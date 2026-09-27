// Package reflection turns finished runs into playbook changes, so jobs get cheaper and faster over time
package reflection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/actor"
	"github.com/italypaleale/francis/builtin/cronjob"
	"github.com/italypaleale/francis/builtin/taskpool"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// Reflection statuses on a run
const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// staleAfter is how long a reflection may stay pending since it was requested or started before the reconciler assumes its task was lost
// Queued tasks survive restarts but share the replica's one slot across every workspace, so runs that finish together can keep one waiting for hours
const staleAfter = 24 * time.Hour

// Playbooks reads and writes job playbooks
type Playbooks interface {
	Current(ctx context.Context, jobID string) (int64, playbook.Content, error)
	Save(ctx context.Context, workspaceID, jobID string, c playbook.Content, meta playbook.VersionMeta) (int64, error)
	RecordStats(ctx context.Context, jobID string, hits []string, scripts map[string]playbook.ScriptStats) error
}

// ImageVerifier builds a Dockerfile and waits for the outcome
type ImageVerifier interface {
	Verify(ctx context.Context, jobID, dockerfile string) error
}

// DemotionChecker tells whether repeated fallbacks took a graduated job back to Assisted
type DemotionChecker interface {
	Demoted(ctx context.Context, workspaceID, jobID string) (bool, error)
}

// SettingsReader reads the workspace's reflection model and spend limit
type SettingsReader interface {
	Get(ctx context.Context, workspaceID string) (settings.WorkspaceSettings, error)
}

type Dependencies struct {
	DB       *database.DB
	Actors   francishost.Host
	Bus      events.Bus
	Storage  storage.FileStorage
	Jobs     runner.JobLoader
	Models   runner.ModelResolver
	Playbook Playbooks
	Settings SettingsReader
	// Images is nil when the sandbox adapter cannot build images, and reflection then never changes the Dockerfile
	Images   ImageVerifier
	Demotion DemotionChecker
	// MaintenanceDisabled skips the reconciler, like Pocket ID does in test mode
	MaintenanceDisabled bool
}

type Module struct {
	deps    Dependencies
	queries *reflectiondb.Queries
	pool    *taskpool.TaskPoolService
	// tester runs a proposed main script before it is applied, and is nil without a sandbox backend
	tester MainTester
}

func New(deps Dependencies) (*Module, error) {
	m := &Module{deps: deps, queries: reflectiondb.New(deps.DB)}

	// One reflection at a time per replica, so learning never competes with runs for capacity
	pool, err := taskpool.New("reflection",
		taskpool.WithHandler(m.handle),
		taskpool.WithConcurrency(1),
		taskpool.WithMaxAttempts(3),
		taskpool.WithLogger(slog.Default().With("scope", "reflection")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create reflection taskpool: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to register reflection taskpool: %w", err)
	}
	m.pool = pool.Service(deps.Actors.Service())

	if !deps.MaintenanceDisabled {
		reconciler, err := cronjob.New("ReflectionReconciler",
			cronjob.WithInterval(10*time.Minute),
			cronjob.WithJob(m.reconcile),
			cronjob.WithLogger(slog.Default()),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create reflection reconciler: %w", err)
		}
		err = deps.Actors.RegisterBuiltInActor(reconciler)
		if err != nil {
			return nil, fmt.Errorf("failed to register reflection reconciler: %w", err)
		}
	}
	return m, nil
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	// Reflection runs in the background, so learning only answers that the request was accepted
	learn := httpserver.Operation("learn-from-run", http.MethodPost, "/api/runs/{id}/learn", "Runs")
	learn.DefaultStatus = http.StatusAccepted
	httpserver.Register(api, learn, auth, m.learn)
}

// RunFinished records the run's toolkit usage and starts reflection when the job learns automatically
func (m *Module) RunFinished(ctx context.Context, run runner.Run, final runner.Final) {
	// Script stats are kept whether or not the job learns, so the toolkit's track record is complete
	if len(final.Toolkit) > 0 {
		stats := make(map[string]playbook.ScriptStats, len(final.Toolkit))
		for name, u := range final.Toolkit {
			stats[name] = playbook.ScriptStats{Calls: u.Calls, Failures: u.Failures}
		}
		err := m.deps.Playbook.RecordStats(ctx, run.JobID, nil, stats)
		if err != nil {
			slog.WarnContext(ctx, "Failed to record toolkit stats", slog.String("run", run.ID), slog.Any("error", err))
		}
	}

	// The runner decided whether this run is learned from and stored it with the final status, so only the task is left to queue
	if final.Reflection != runner.ReflectionPending {
		return
	}
	err := m.submit(ctx, run.WorkspaceID, run.JobID, run.ID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to start reflection", slog.String("run", run.ID), slog.Any("error", err))
	}
}

type learnInput struct {
	ID string `path:"id"`
}

// learn reflects on one run on request, the "Learn from this run" button, which works whether or not the job learns automatically
func (m *Module) learn(ctx context.Context, in *learnInput) (*struct{}, error) {
	wid := principal.WorkspaceID(ctx)
	run, err := m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Run")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load run: %w", err)
	}
	switch {
	case run.Reflection == StatusPending:
		return nil, apperror.Conflict("Reflection on this run is already in progress")
	case run.Status != runner.StatusSucceeded && run.Status != runner.StatusFailed && run.Status != runner.StatusTimedOut:
		return nil, apperror.Conflict("Only finished runs that succeeded, failed or timed out can be learned from")
	}

	return nil, m.start(ctx, wid, run.JobID, run.ID)
}

// start marks a run's reflection as pending and queues it
func (m *Module) start(ctx context.Context, workspaceID, jobID, runID string) error {
	now := database.Now()
	n, err := m.queries.MarkPending(ctx, reflectiondb.MarkPendingParams{RequestedAt: &now, WorkspaceID: workspaceID, ID: runID})
	if err != nil {
		return fmt.Errorf("failed to mark reflection pending: %w", err)
	}
	if n == 0 {
		return apperror.Conflict("Reflection on this run is already in progress")
	}
	return m.submit(ctx, workspaceID, jobID, runID)
}

// submit queues the reflection of a run that is already marked pending
func (m *Module) submit(ctx context.Context, workspaceID, jobID, runID string) error {
	// Each request gets its own task, since a finished task's key would swallow a later "Learn from this run"
	_, err := m.pool.Submit(ctx, task{WorkspaceID: workspaceID, RunID: runID}, taskpool.WithTaskKey(fmt.Sprintf("%s-%d", runID, time.Now().UnixMilli())))
	if err != nil {
		m.finish(context.WithoutCancel(ctx), workspaceID, jobID, runID, nil, result{Status: StatusFailed, Error: "Reflection could not be queued"})
		return fmt.Errorf("failed to queue reflection: %w", err)
	}
	m.publish(ctx, workspaceID, jobID, runID, StatusPending)
	return nil
}

type task struct {
	WorkspaceID string `json:"workspaceId"`
	RunID       string `json:"runId"`
}

func (m *Module) handle(ctx context.Context, t taskpool.Task) error {
	var in task
	err := t.Decode(&in)
	if err != nil {
		return err
	}
	run, err := m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: in.WorkspaceID, ID: in.RunID})
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if run.Reflection != StatusPending {
		return nil
	}

	// Claiming the request restarts the reconciler's clock, and a task that lost the claim leaves the reflection to the one that took it
	claimedAt := database.Now()
	n, err := m.queries.ClaimReflection(ctx, reflectiondb.ClaimReflectionParams{ClaimedAt: &claimedAt, RequestedAt: run.ReflectionRequestedAt, WorkspaceID: run.WorkspaceID, ID: run.ID})
	if err != nil {
		return fmt.Errorf("failed to claim reflection: %w", err)
	}
	if n == 0 {
		return nil
	}
	run.ReflectionRequestedAt = &claimedAt

	res, err := m.reflect(ctx, run)

	// A reflection interrupted by a shutdown says nothing about the run, so the taskpool retries it
	if err != nil && (ctx.Err() != nil || errors.Is(err, actor.ErrActorHalted)) {
		return fmt.Errorf("reflection interrupted: %w", err)
	}
	if err != nil {
		slog.WarnContext(ctx, "Reflection failed", slog.String("run", run.ID), slog.Any("error", err))
		res.Status, res.Error = StatusFailed, err.Error()
	}
	m.finish(context.WithoutCancel(ctx), run.WorkspaceID, run.JobID, run.ID, run.ReflectionRequestedAt, res)
	return nil
}

// result is the outcome of one reflection, stored on the run
type result struct {
	Status  string
	Error   string
	Summary string
	Ops     []playbook.AppliedOp
	Version *int64
	// Cost is what the reflection's model calls and shadow runs cost in micro-USD, and Tokens their input and output tokens
	Cost   int64
	Tokens int64
}

// finish records a reflection's result on the run
// requestedAt identifies the request this reflection answers, so a result arriving after a newer request only adds its cost and tokens
func (m *Module) finish(ctx context.Context, workspaceID, jobID, runID string, requestedAt *int64, res result) {
	// A free model still used tokens, which a workspace that shows usage in tokens counts
	if res.Cost > 0 || res.Tokens > 0 {
		err := m.queries.AddReflectionSpend(ctx, reflectiondb.AddReflectionSpendParams{Cost: res.Cost, Tokens: res.Tokens, WorkspaceID: workspaceID, ID: runID})
		if err != nil {
			slog.ErrorContext(ctx, "Failed to record the reflection cost", slog.String("run", runID), slog.Any("error", err))
		}
	}

	params := reflectiondb.FinishReflectionParams{
		RequestedAt:       requestedAt,
		Reflection:        res.Status,
		ReflectionError:   nilIfEmpty(res.Error),
		ReflectionSummary: nilIfEmpty(res.Summary),
		ReflectionVersion: res.Version,
		WorkspaceID:       workspaceID,
		ID:                runID,
	}
	if len(res.Ops) > 0 {
		raw, _ := json.Marshal(res.Ops)
		params.ReflectionOps = new(string(raw))
	}
	n, err := m.queries.FinishReflection(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to record the reflection result", slog.String("run", runID), slog.Any("error", err))
		return
	}

	// A result that was too late changed nothing that open pages show
	if n > 0 {
		m.publish(ctx, workspaceID, jobID, runID, res.Status)
	}
}

// publish tells open pages that a run's reflection changed, so the run page and the playbook can refresh
func (m *Module) publish(ctx context.Context, workspaceID, jobID, runID, status string) {
	events.PublishWorkspace(ctx, m.deps.Bus, workspaceID, map[string]any{"kind": "reflection", "jobId": jobID, "runId": runID, "status": status})
}

// reconcile fails reflections whose task was lost, so they can be started again from the run page
func (m *Module) reconcile(ctx context.Context) error {
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	n, err := m.queries.FailStalePending(ctx, &cutoff)
	if err != nil {
		return fmt.Errorf("failed to fail stale reflections: %w", err)
	}
	if n > 0 {
		slog.WarnContext(ctx, "Failed reflections that did not finish", slog.Int64("count", n))
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
