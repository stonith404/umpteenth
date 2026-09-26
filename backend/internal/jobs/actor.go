package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/italypaleale/francis/actor"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
)

const (
	actorType = "job"

	methodTrigger      = "trigger"
	methodRunFinished  = "runFinished"
	methodReschedule   = "reschedule"
	methodFireSchedule = "fireSchedule"
)

// actorState is the durable state of one job actor
type actorState struct {
	// Active holds runs submitted to the taskpool and not yet finished
	Active []string `json:"active"`
	// Queue holds runs created under the queue policy that wait for the active run to finish
	Queue []string `json:"queue"`
	// Alarm is the name of the pending schedule alarm, unique per occurrence so rescheduling from inside an alarm is safe
	Alarm string `json:"alarm"`

	// waiting lists active runs that have not started yet, which a lost submission could have left behind
	waiting []string
}

type triggerInput struct {
	WorkspaceID string              `json:"workspaceId"`
	Request     runs.TriggerRequest `json:"request"`
}

type runFinishedInput struct {
	RunID string `json:"runId"`
}

// jobActor serializes everything that decides when a job runs: its schedule and its concurrency policy (PLAN.md §3.3)
// Because Francis runs one turn per actor at a time, schedule, webhook and manual triggers can never race each other
type jobActor struct {
	id     string
	client actor.Client[actorState]
	m      *Module
}

func (m *Module) newActor(actorID string, svc *actor.Service) actor.Actor {
	return &jobActor{id: actorID, client: actor.NewActorClient[actorState](actorType, actorID, svc), m: m}
}

func (a *jobActor) Invoke(ctx context.Context, method string, data actor.Envelope) (any, error) {
	switch method {
	case methodTrigger:
		var in triggerInput
		if err := data.Decode(&in); err != nil {
			return nil, err
		}
		return a.trigger(ctx, in.Request)
	case methodRunFinished:
		var in runFinishedInput
		if err := data.Decode(&in); err != nil {
			return nil, err
		}
		return nil, a.runFinished(ctx, in.RunID)
	case methodReschedule:
		return nil, a.reschedule(ctx)
	case methodFireSchedule:
		return nil, a.fireSchedule(ctx)
	default:
		return nil, fmt.Errorf("unknown job actor method %q", method)
	}
}

func (a *jobActor) Alarm(ctx context.Context, _ string, _ actor.Envelope) error {
	return a.fireSchedule(ctx)
}

// load returns the job and the actor state reconciled against the runs table
// Reconciling on every turn means a crashed replica or lost notification can never leave the job stuck
func (a *jobActor) load(ctx context.Context) (jobsdb.Job, actorState, error) {
	job, err := a.m.queries.GetJobUnscoped(ctx, a.id)
	if err != nil {
		return jobsdb.Job{}, actorState{}, err
	}
	state, err := a.client.GetState(ctx)
	if err != nil {
		return jobsdb.Job{}, actorState{}, fmt.Errorf("failed to load job actor state: %w", err)
	}

	live, err := a.m.queries.ListActiveRunsForJob(ctx, jobsdb.ListActiveRunsForJobParams{WorkspaceID: job.WorkspaceID, JobID: job.ID})
	if err != nil {
		return jobsdb.Job{}, actorState{}, fmt.Errorf("failed to load active runs: %w", err)
	}
	status := make(map[string]string, len(live))
	for _, r := range live {
		status[r.ID] = r.Status
	}

	state.Active = slices.DeleteFunc(state.Active, func(id string) bool { _, ok := status[id]; return !ok })
	state.Queue = slices.DeleteFunc(state.Queue, func(id string) bool { return status[id] != runner.StatusQueued })

	// Runs the state doesn't know about count as active, which errs on the side of never running twice
	for id := range status {
		if !slices.Contains(state.Active, id) && !slices.Contains(state.Queue, id) {
			state.Active = append(state.Active, id)
		}
	}
	for _, id := range state.Active {
		if status[id] == runner.StatusQueued {
			state.waiting = append(state.waiting, id)
		}
	}
	return job, state, nil
}

// resume restarts work a failed submission or a lost finish notification left waiting, so a job can never get stuck
// Submitting is idempotent per run, so a run that is already waiting in the pool is not enqueued twice
func (a *jobActor) resume(ctx context.Context, job jobsdb.Job, state *actorState) error {
	for _, id := range state.waiting {
		err := a.m.runs.Submit(ctx, id)
		if err != nil {
			return err
		}
	}

	// Nothing active but runs waiting in the queue means the finish notification of the last run never arrived
	if len(state.Active) == 0 && len(state.Queue) > 0 && job.ArchivedAt == nil {
		next := state.Queue[0]
		state.Queue = state.Queue[1:]
		state.Active = append(state.Active, next)
		err := a.save(ctx, *state)
		if err != nil {
			return err
		}
		return a.m.runs.Submit(ctx, next)
	}
	return nil
}

func (a *jobActor) save(ctx context.Context, state actorState) error {
	return a.client.SetState(ctx, state, nil)
}

func (a *jobActor) trigger(ctx context.Context, req runs.TriggerRequest) (runs.TriggerResult, error) {
	job, state, err := a.load(ctx)
	if database.IsNotFound(err) || (err == nil && job.ArchivedAt != nil) {
		return runs.TriggerResult{}, apperror.NotFound("Job")
	} else if err != nil {
		return runs.TriggerResult{}, err
	}

	// Automatic triggers respect the enabled switch, manual ones always work
	if !job.Enabled && (req.Trigger == TriggerSchedule || req.Trigger == TriggerWebhook) {
		return runs.TriggerResult{Status: runner.StatusSkipped}, nil
	}

	// Repair a stuck slot first, so the decision below sees what is really running
	err = a.resume(ctx, job, &state)
	if err != nil {
		slog.WarnContext(ctx, "Failed to resume waiting runs", slog.String("job", job.ID), slog.Any("error", err))
	}

	newRun, err := a.m.newRun(ctx, job, req)
	if err != nil {
		return runs.TriggerResult{}, err
	}
	// Under the queue policy a new trigger waits behind runs already queued, so the order of triggers is kept
	busy := len(state.Active) > 0 || (job.Concurrency == ConcurrencyQueue && len(state.Queue) > 0)

	switch {
	case busy && job.Concurrency == ConcurrencySkip:
		// Skipped runs are recorded so the history shows that a trigger was declined
		newRun.Status = runner.StatusSkipped
		newRun.Error = "Skipped because another run of this job was still active"
		id, err := a.m.runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		return runs.TriggerResult{RunID: id, Status: runner.StatusSkipped}, nil

	case busy && job.Concurrency == ConcurrencyQueue:
		id, err := a.m.runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		state.Queue = append(state.Queue, id)
		return runs.TriggerResult{RunID: id, Status: runner.StatusQueued}, a.save(ctx, state)

	default:
		id, err := a.m.runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		// State is saved before submitting, so a crash in between leaves a run the reconciler treats as active rather than a lost slot
		state.Active = append(state.Active, id)
		err = a.save(ctx, state)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		return runs.TriggerResult{RunID: id, Status: runner.StatusQueued}, a.m.runs.Submit(ctx, id)
	}
}

// runFinished releases the concurrency slot and starts the next queued run
func (a *jobActor) runFinished(ctx context.Context, runID string) error {
	job, state, err := a.load(ctx)
	if err != nil {
		return err
	}
	state.Active = slices.DeleteFunc(state.Active, func(id string) bool { return id == runID })
	state.Queue = slices.DeleteFunc(state.Queue, func(id string) bool { return id == runID })
	state.waiting = slices.DeleteFunc(state.waiting, func(id string) bool { return id == runID })

	// Start the next queued run, or resubmit one a failed submission left waiting
	err = a.resume(ctx, job, &state)
	if err != nil {
		return err
	}
	return a.save(ctx, state)
}

// reschedule sets the schedule alarm from the job's current cron, or removes it
func (a *jobActor) reschedule(ctx context.Context) error {
	job, err := a.m.queries.GetJobUnscoped(ctx, a.id)
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	state, err := a.client.GetState(ctx)
	if err != nil {
		return err
	}

	// Drop the pending alarm first; a new one is set below if the job still has a schedule
	if state.Alarm != "" {
		_ = a.client.DeleteAlarm(ctx, state.Alarm)
		state.Alarm = ""
	}

	var nextAt *int64
	if job.ArchivedAt == nil && job.Enabled && job.Cron != nil && *job.Cron != "" {
		tz := ""
		if job.Timezone != nil {
			tz = *job.Timezone
		}
		next, err := nextRun(*job.Cron, tz, time.Now())
		if err != nil {
			slog.WarnContext(ctx, "Job has an invalid schedule", slog.String("job", job.ID), slog.Any("error", err))
		} else {
			state.Alarm = fmt.Sprintf("schedule-%d", next.Unix())
			err = a.client.SetAlarm(ctx, state.Alarm, actor.AlarmProperties{DueTime: next})
			if err != nil {
				return fmt.Errorf("failed to set schedule alarm: %w", err)
			}
			nextAt = new(next.UnixMilli())
		}
	}

	// The alarm is remembered before anything else can fail, so a retry deletes it instead of arming a second one
	err = a.save(ctx, state)
	if err != nil {
		return err
	}

	// The next run time only feeds the UI, so failing to store it must not undo the armed schedule
	err = a.m.queries.SetNextRunAt(ctx, jobsdb.SetNextRunAtParams{WorkspaceID: job.WorkspaceID, ID: job.ID, NextRunAt: nextAt})
	if err != nil {
		slog.WarnContext(ctx, "Failed to store the next run time", slog.String("job", job.ID), slog.Any("error", err))
	}
	return nil
}

// fireSchedule runs the job for its schedule and arms the next occurrence
// A missed occurrence (e.g. during downtime) fires once when the alarm is delivered, and is never replayed multiple times
func (a *jobActor) fireSchedule(ctx context.Context) error {
	// The fired alarm is gone once this turn completes, so forget it before rescheduling
	state, err := a.client.GetState(ctx)
	if err != nil {
		return err
	}
	state.Alarm = ""
	err = a.save(ctx, state)
	if err != nil {
		return err
	}

	res, err := a.trigger(ctx, runs.TriggerRequest{Trigger: TriggerSchedule})
	if err != nil && !apperror.IsCode(err, apperror.CodeNotFound) {
		slog.ErrorContext(ctx, "Scheduled run failed to start", slog.String("job", a.id), slog.Any("error", err))
	} else if err == nil {
		slog.InfoContext(ctx, "Scheduled run triggered", slog.String("job", a.id), slog.String("run", res.RunID), slog.String("status", res.Status))
	}

	// The occurrence has fired, so an error must not make Francis redeliver the alarm and trigger the job again
	err = a.reschedule(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to arm the next scheduled run", slog.String("job", a.id), slog.Any("error", err))
	}
	return nil
}

// placementRetryBackoff spans a replica restart, during which the cluster can briefly route an actor to a host that no longer owns it
var placementRetryBackoff = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

// invoke calls a method on a job actor, wherever it is placed in the cluster
func (m *Module) invoke(ctx context.Context, jobID, method string, data any, out any) error {
	for attempt := 0; ; attempt++ {
		env, err := m.actors.Service().Invoke(ctx, actorType, jobID, method, data)

		// Placement errors happen before the method runs, so repeating the call cannot run it twice
		placement := errors.Is(err, actor.ErrActorNotHosted) || errors.Is(err, actor.ErrActorHalted) || errors.Is(err, actor.ErrActorNotActive)
		if placement && attempt < len(placementRetryBackoff) {
			slog.DebugContext(ctx, "Job actor placement changed, retrying", slog.String("job", jobID), slog.Any("error", err))
			select {
			case <-ctx.Done():
				return err
			case <-time.After(placementRetryBackoff[attempt]):
			}
			continue
		}
		if err != nil {
			return err
		}
		if out != nil && env != nil {
			return env.Decode(out)
		}
		return nil
	}
}
