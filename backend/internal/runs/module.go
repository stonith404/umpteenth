// Package runs owns run records, the durable run queue, cancellation, live timelines and the reconciler
package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/builtin/cronjob"
	"github.com/italypaleale/francis/builtin/signal"
	"github.com/italypaleale/francis/builtin/taskpool"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// Triggerer starts a new run of a job through its actor, used for retries
type Triggerer interface {
	Trigger(ctx context.Context, workspaceID, jobID string, req TriggerRequest) (TriggerResult, error)
}

// Run triggers
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	TriggerWebhook  = "webhook"
	TriggerAPI      = "api"
	TriggerRetry    = "retry"
)

// TriggerRequest asks a job for a new run
type TriggerRequest struct {
	Trigger      string          `json:"trigger"`
	TriggeredBy  *string         `json:"triggeredBy,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Instructions string          `json:"instructions,omitempty"`
}

// TriggerResult tells the caller what happened to the trigger
type TriggerResult struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

type Dependencies struct {
	DB                *database.DB
	Actors            francishost.Host
	Bus               events.Bus
	Storage           storage.FileStorage
	Adapter           sandbox.Adapter
	MaxConcurrentRuns int
	// MaintenanceDisabled skips the reconciler and retention cron jobs, e.g. in test mode
	MaintenanceDisabled bool
	// RetentionDays returns the event retention of a workspace, and is required unless maintenance is disabled
	RetentionDays func(ctx context.Context, workspaceID string) int
}

type Module struct {
	deps    Dependencies
	queries *runsdb.Queries
	store   *store

	pool     *taskpool.TaskPoolService
	cancels  *signal.SignalService
	runner   *runner.Runner
	jobs     Triggerer
	notifier runner.Notifier
}

func New(deps Dependencies) (*Module, error) {
	m := &Module{deps: deps, queries: runsdb.New(deps.DB)}
	m.store = &store{queries: m.queries}

	// The run queue is a Francis taskpool, so queued runs survive restarts and spread across replicas
	pool, err := taskpool.New("runs",
		taskpool.WithHandler(m.handleTask),
		// A replica without a sandbox adapter has no runner, and declining a run hands it to a replica that has one without spending an attempt
		taskpool.WithAccept(func(context.Context, taskpool.Task) bool { return m.runner != nil }),
		taskpool.WithConcurrency(deps.MaxConcurrentRuns),
		taskpool.WithMaxAttempts(3),
		taskpool.WithLogger(slog.Default().With("scope", "runs")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create run taskpool: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to register run taskpool: %w", err)
	}
	m.pool = pool.Service(deps.Actors.Service())

	// Cancellation is a Francis signal, so a cancel reaches the executing replica from any replica
	sig, err := signal.New("run-cancel", signal.WithRetention(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("failed to create cancel signal: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(sig)
	if err != nil {
		return nil, fmt.Errorf("failed to register cancel signal: %w", err)
	}
	m.cancels = sig.Service(deps.Actors.Service())

	if !deps.MaintenanceDisabled {
		err = m.registerCronJobs()
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Store exposes the run store to the runner
func (m *Module) Store() runner.RunStore { return m.store }

// SetRunner wires the runner, which is built after the modules it depends on
func (m *Module) SetRunner(r *runner.Runner) { m.runner = r }

// SetJobs wires the job trigger used for retries and the notifier that releases a job's concurrency slot
func (m *Module) SetJobs(t Triggerer, n runner.Notifier) {
	m.jobs = t
	m.notifier = n
}

// WaitCancel implements runner.CancelWaiter with the Francis signal
func (m *Module) WaitCancel(ctx context.Context, runID string) error {
	_, err := m.cancels.Wait(ctx, runID)
	return err
}

type runTask struct {
	RunID string `json:"runId"`
}

func (m *Module) handleTask(ctx context.Context, task taskpool.Task) error {
	var t runTask
	err := task.Decode(&t)
	if err != nil {
		return fmt.Errorf("invalid run task: %w", err)
	}
	return m.runner.Execute(ctx, t.RunID)
}

// NewRun describes a run to create
type NewRun struct {
	WorkspaceID     string
	JobID           string
	Number          int64
	Mode            string
	Trigger         string
	TriggeredBy     *string
	Input           json.RawMessage
	Instructions    string
	PlaybookVersion int64
	ModelID         *string
	// Status is queued, or skipped when the concurrency policy declined the trigger
	Status string
	Error  string
}

// Create inserts a run row without submitting it
func (m *Module) Create(ctx context.Context, n NewRun) (string, error) {
	id := database.NewID()
	now := database.Now()
	// A webhook or API body passes json.Valid even with invalid UTF-8 inside its strings, which Postgres refuses in a text column
	// Invalid bytes can only sit inside JSON strings, so replacing them keeps the input valid JSON
	var input *string
	if len(n.Input) > 0 {
		input = new(validText(string(n.Input)))
	}
	var finishedAt *int64
	if runner.IsTerminal(n.Status) {
		finishedAt = &now
	}
	err := m.queries.CreateRun(ctx, runsdb.CreateRunParams{
		ID:              id,
		WorkspaceID:     n.WorkspaceID,
		JobID:           n.JobID,
		Number:          n.Number,
		Status:          n.Status,
		Mode:            n.Mode,
		Trigger:         n.Trigger,
		TriggeredBy:     n.TriggeredBy,
		Input:           input,
		Instructions:    nilIfEmpty(validText(n.Instructions)),
		PlaybookVersion: n.PlaybookVersion,
		ModelID:         n.ModelID,
		QueuedAt:        now,
		Error:           nilIfEmpty(n.Error),
		FinishedAt:      finishedAt,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create run: %w", err)
	}
	events.PublishWorkspace(ctx, m.deps.Bus, n.WorkspaceID, map[string]any{"kind": "run", "runId": id, "jobId": n.JobID, "status": n.Status})
	return id, nil
}

// Submit enqueues a queued run on the taskpool; the run ID is the task key, so submitting twice is harmless
func (m *Module) Submit(ctx context.Context, runID string) error {
	_, err := m.pool.Submit(ctx, runTask{RunID: runID}, taskpool.WithTaskKey(runID))
	if err != nil {
		return fmt.Errorf("failed to submit run: %w", err)
	}
	return nil
}

// Cancel stops a run: a queued run is cancelled right away, a live one is signaled wherever it executes
func (m *Module) Cancel(ctx context.Context, workspaceID, runID string) error {
	run, err := m.getRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}

	// A queued run gets its final event before its terminal status, so a stream that sees the status also finds the event
	if run.Status == runner.StatusQueued {
		final := runner.Final{Status: runner.StatusCancelled, Error: "Cancelled before it started"}
		m.recordEnd(ctx, runID, final)
		n, err := m.queries.CancelQueuedRun(ctx, runsdb.CancelQueuedRunParams{WorkspaceID: workspaceID, ID: runID, FinishedAt: new(database.Now())})
		if err != nil {
			return fmt.Errorf("failed to cancel run: %w", err)
		}
		if n == 1 {
			run.Status = final.Status
			if m.notifier != nil {
				m.notifier.RunFinished(ctx, toRunnerRun(run), final)
			}
			m.announceEnd(ctx, toRunnerRun(run), final)
			return nil
		}
		// A runner claimed the run in the meantime, so it is cancelled through the signal below and records its own final status
	}

	// A live run is flagged and signaled, and its runner records how it ended
	n, err := m.queries.RequestCancel(ctx, runsdb.RequestCancelParams{WorkspaceID: workspaceID, ID: runID})
	if err != nil {
		return fmt.Errorf("failed to request cancel: %w", err)
	}
	if n == 0 {
		return apperror.Conflict("The run is not running")
	}
	return m.cancels.Complete(ctx, runID, nil)
}

func (m *Module) registerCronJobs() error {
	// The reconciler fails runs whose replica stopped sending heartbeats
	reconciler, err := cronjob.New("RunReconciler",
		cronjob.WithInterval(time.Minute),
		cronjob.WithJob(m.reconcile),
		cronjob.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to create run reconciler: %w", err)
	}
	err = m.deps.Actors.RegisterBuiltInActor(reconciler)
	if err != nil {
		return fmt.Errorf("failed to register run reconciler: %w", err)
	}

	retention, err := cronjob.New("RetentionPrune",
		cronjob.WithCron("17 3 * * *"),
		cronjob.WithJob(m.prune),
		cronjob.WithJitter(10*time.Minute),
		cronjob.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to create retention job: %w", err)
	}
	return m.deps.Actors.RegisterBuiltInActor(retention)
}

// staleAfter is how long a run may go without a heartbeat before it counts as interrupted
const staleAfter = 60 * time.Second

func (m *Module) reconcile(ctx context.Context) error {
	stale, err := m.queries.ListStaleRuns(ctx, new(time.Now().Add(-staleAfter).UnixMilli()))
	if err != nil {
		return fmt.Errorf("failed to list stale runs: %w", err)
	}
	for _, s := range stale {
		// The final event goes in before the terminal status, like the runner does, so a stream that sees the status also finds the event
		final := runner.Final{Status: runner.StatusFailed, Error: "interrupted: the replica executing this run stopped responding"}
		m.recordEnd(ctx, s.ID, final)
		ok, err := m.store.Finish(ctx, s.ID, final)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to mark run as interrupted", slog.String("run", s.ID), slog.Any("error", err))
			continue
		}
		if !ok {
			continue
		}
		slog.WarnContext(ctx, "Marked run as interrupted", slog.String("run", s.ID))

		// A sandbox this cannot destroy, e.g. one on another replica's host, is reaped there once its run is terminal
		if s.SandboxID != nil && m.deps.Adapter != nil {
			_ = m.deps.Adapter.Destroy(ctx, *s.SandboxID)
		}
		run, err := m.store.Load(ctx, s.ID)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to load an interrupted run", slog.String("run", s.ID), slog.Any("error", err))
			continue
		}
		if m.notifier != nil {
			m.notifier.RunFinished(ctx, run, final)
		}
		m.announceEnd(ctx, run, final)
	}
	return nil
}

// recordEnd persists how a run ended when no runner was there to say it
// Callers run it before writing the terminal status, because streams send their end marker as soon as they see that status
func (m *Module) recordEnd(ctx context.Context, runID string, final runner.Final) {
	events.NewRecorder(m.deps.DB, m.deps.Bus, m.deps.Storage, runID).
		Emit(ctx, events.Event{Type: events.TypeRunStatus, Payload: map[string]any{"status": final.Status, "error": final.Error}})
}

// announceEnd tells open timelines and tables that a run ended, after its terminal status was written
func (m *Module) announceEnd(ctx context.Context, run runner.Run, final runner.Final) {
	raw, _ := json.Marshal(events.Message{Kind: "status", Status: final.Status})
	_ = m.deps.Bus.Publish(ctx, events.RunTopic(run.ID), raw)
	events.PublishWorkspace(ctx, m.deps.Bus, run.WorkspaceID, map[string]any{"kind": "run", "runId": run.ID, "jobId": run.JobID, "status": final.Status})
}

// pruneWindow is how far each prune overlaps the range the previous one covered
// Earlier runs were pruned by previous nights, and the extra days cover nights the job was skipped
const pruneWindow = 3 * 24 * time.Hour

const (
	// pruneBurst is how long a prune deletes without a break
	pruneBurst = 100 * time.Millisecond
	// prunePause outlasts the longest sleep of SQLite's busy handler, 100 ms, so every writer that waited during a burst gets the write lock before the next one
	prunePause = 150 * time.Millisecond
)

// prune deletes events and blobs of runs older than each workspace's retention
// Run rows are kept forever for the trend charts
func (m *Module) prune(ctx context.Context) error {
	workspaces, err := m.queries.ListWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list workspaces: %w", err)
	}
	for _, wid := range workspaces {
		m.pruneWorkspace(ctx, wid, time.Now().AddDate(0, 0, -m.deps.RetentionDays(ctx, wid)))
	}
	return nil
}

// pruneWorkspace deletes the events and blobs of runs that finished in the prune window before the cutoff
func (m *Module) pruneWorkspace(ctx context.Context, wid string, cutoff time.Time) {
	// Each prune starts where the last one ended, with some overlap, so nightly runs don't revisit all history
	// Starting from the watermark instead of a fixed window also catches up after missed nights or a lowered retention
	key := "retention-watermark/" + wid
	before := cutoff.UnixMilli()
	after := int64(0)
	if raw, err := m.queries.GetPruneWatermark(ctx, key); err == nil {
		if last, err := strconv.ParseInt(raw, 10, 64); err == nil {
			after = min(last, before) - pruneWindow.Milliseconds()
		}
	} else if !database.IsNotFound(err) {
		slog.WarnContext(ctx, "Failed to load the retention watermark", slog.String("workspace", wid), slog.Any("error", err))
		return
	}
	ids, err := m.queries.ListFinishedRunIDsBefore(ctx, runsdb.ListFinishedRunIDsBeforeParams{WorkspaceID: wid, Before: &before, After: &after})
	if err != nil {
		slog.WarnContext(ctx, "Failed to list runs to prune", slog.String("workspace", wid), slog.Any("error", err))
		return
	}

	// Deletes go one run at a time in short bursts, since a single delete over a large window, e.g. after the retention was lowered, holds SQLite's write lock long enough to fail other writers such as the actor host's health check
	burst := time.Now()
	pace := func() bool {
		if time.Since(burst) < pruneBurst {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(prunePause):
		}
		burst = time.Now()
		return true
	}

	// Blobs go first, then the events of the same runs
	// A failed delete ends the pass before the watermark moves, since later passes only look a few days back and would never visit these files again
	for _, id := range ids {
		if !pace() {
			return
		}
		if err := m.deps.Storage.DeleteAll(ctx, "runs/"+id); err != nil {
			slog.WarnContext(ctx, "Failed to prune run files", slog.String("run", id), slog.Any("error", err))
			return
		}
	}
	for _, id := range ids {
		if !pace() {
			return
		}
		err = m.queries.DeleteRunEventsOf(ctx, runsdb.DeleteRunEventsOfParams{WorkspaceID: wid, ID: id})
		if err != nil {
			slog.WarnContext(ctx, "Failed to prune run events", slog.String("run", id), slog.Any("error", err))
			return
		}
	}

	// The watermark only moves after a complete pass, so a failed night is retried in full
	err = m.queries.SetPruneWatermark(ctx, runsdb.SetPruneWatermarkParams{Key: key, Value: strconv.FormatInt(before, 10)})
	if err != nil {
		slog.WarnContext(ctx, "Failed to store the retention watermark", slog.String("workspace", wid), slog.Any("error", err))
	}
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	// Every member works with runs, and only admins delete them, since that erases the transcript and the usage for everyone
	admin := httpserver.Access{MinRole: principal.RoleAdmin}
	httpserver.Register(api, httpserver.Operation("list-runs", http.MethodGet, "/api/runs", "Runs"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("get-run", http.MethodGet, "/api/runs/{id}", "Runs"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("list-run-events", http.MethodGet, "/api/runs/{id}/events", "Runs"), auth, m.listEvents)
	httpserver.Register(api, httpserver.Operation("cancel-run", http.MethodPost, "/api/runs/{id}/cancel", "Runs"), auth, m.cancel)
	httpserver.Register(api, httpserver.Operation("retry-run", http.MethodPost, "/api/runs/{id}/retry", "Runs"), auth, m.retry)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-run", http.MethodDelete, "/api/runs/{id}", "Runs"), admin), auth, m.deleteOne)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-runs", http.MethodPost, "/api/runs/delete", "Runs"), admin), auth, m.deleteMany)
	httpserver.Register(api, httpserver.Operation("list-run-artifacts", http.MethodGet, "/api/runs/{id}/artifacts", "Runs"), auth, m.listArtifacts)
	httpserver.Register(api, httpserver.Operation("get-run-artifact", http.MethodGet, "/api/runs/{id}/artifact", "Runs"), auth, m.getArtifact)
	m.registerStreams(api, auth)
}

// RunIDByBrokerToken resolves a live run from its broker token hash, used by the broker
func (m *Module) RunIDByBrokerToken(ctx context.Context, tokenHash string) (string, error) {
	return m.queries.GetRunByBrokerToken(ctx, &tokenHash)
}

const (
	// orphanGrace keeps the reaper away from sandboxes that are still being set up or torn down
	orphanGrace = 15 * time.Minute
	// maxSandboxAge bounds any sandbox, even one whose run still looks live
	maxSandboxAge = 25 * time.Hour
)

// ReapSandboxes destroys sandboxes on this replica's backend whose run is over, unknown, or far too old
// It runs on every replica because sandbox backends can be host-local, and every decision comes from the database
func (m *Module) ReapSandboxes(ctx context.Context) {
	if m.deps.Adapter == nil {
		return
	}
	list, err := m.deps.Adapter.List(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to list sandboxes for reaping", slog.Any("error", err))
		return
	}

	now := time.Now()
	for _, sb := range list {
		age := now.Sub(sb.CreatedAt)
		reap := false
		switch run, err := m.queries.GetRunUnscoped(ctx, sb.RunID); {
		case database.IsNotFound(err):
			// Test sandboxes and sandboxes of deleted runs have no live run to protect
			reap = age > orphanGrace
		case err != nil:
			// A failed lookup says nothing about the run, and guessing could destroy a live run's sandbox
			slog.WarnContext(ctx, "Failed to look up the run of a sandbox", slog.String("sandbox", sb.ID), slog.Any("error", err))
		case runner.IsTerminal(run.Status):
			reap = true
		default:
			reap = age > maxSandboxAge
		}
		if !reap {
			continue
		}
		err := m.deps.Adapter.Destroy(ctx, sb.ID)
		if err != nil {
			slog.WarnContext(ctx, "Failed to reap sandbox", slog.String("sandbox", sb.ID), slog.Any("error", err))
			continue
		}
		slog.InfoContext(ctx, "Reaped sandbox", slog.String("sandbox", sb.ID), slog.String("run", sb.RunID))
	}
}
