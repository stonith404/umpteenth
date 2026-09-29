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
	methodForget       = "forget"
)

// actorState is the durable state of one job actor
type actorState struct {
	// Active holds runs submitted to the taskpool and not yet finished
	Active []string `json:"active"`
	// Queue holds runs created under the queue policy that wait for the active run to finish
	Queue []string `json:"queue"`
	// Alarm is the name of the pending schedule alarm, unique per occurrence so rescheduling from inside an alarm is safe
	Alarm string `json:"alarm"`
}

// jobActor serializes everything that decides when a job runs: its schedule and its concurrency policy
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
		var req runs.TriggerRequest
		if err := data.Decode(&req); err != nil {
			return nil, err
		}
		return a.trigger(ctx, req)
	case methodRunFinished:
		return nil, a.runFinished(ctx)
	case methodReschedule:
		return nil, a.reschedule(ctx)
	case methodFireSchedule:
		return nil, a.fireSchedule(ctx, "")
	case methodForget:
		return nil, a.forget(ctx)
	default:
		return nil, fmt.Errorf("unknown job actor method %q", method)
	}
}

func (a *jobActor) Alarm(ctx context.Context, name string, _ actor.Envelope) error {
	return a.fireSchedule(ctx, name)
}

// load returns the job, the actor state reconciled against the runs table, and the active runs that have not started yet
// Reconciling on every turn means a crashed replica or lost notification can never leave the job stuck
func (a *jobActor) load(ctx context.Context) (job jobsdb.Job, state actorState, waiting []string, err error) {
	job, err = a.m.queries.GetJobUnscoped(ctx, a.id)
	if err != nil {
		return jobsdb.Job{}, actorState{}, nil, err
	}
	state, err = a.client.GetState(ctx)
	if err != nil {
		return jobsdb.Job{}, actorState{}, nil, fmt.Errorf("failed to load job actor state: %w", err)
	}

	live, err := a.m.queries.ListActiveRunsForJob(ctx, jobsdb.ListActiveRunsForJobParams{WorkspaceID: job.WorkspaceID, JobID: job.ID})
	if err != nil {
		return jobsdb.Job{}, actorState{}, nil, fmt.Errorf("failed to load active runs: %w", err)
	}
	status := make(map[string]string, len(live))
	for _, r := range live {
		status[r.ID] = r.Status
	}

	// Francis hands out its in-memory copy of the state, so the lists are cloned before they are filtered in place
	state.Active = slices.DeleteFunc(slices.Clone(state.Active), func(id string) bool { _, ok := status[id]; return !ok })
	state.Queue = slices.DeleteFunc(slices.Clone(state.Queue), func(id string) bool { return status[id] != runner.StatusQueued })

	// Runs the state doesn't know about count as active, which errs on the side of never running twice
	for id := range status {
		if !slices.Contains(state.Active, id) && !slices.Contains(state.Queue, id) {
			state.Active = append(state.Active, id)
		}
	}
	for _, id := range state.Active {
		if status[id] == runner.StatusQueued {
			waiting = append(waiting, id)
		}
	}
	return job, state, waiting, nil
}

// resume resubmits active runs that have not started, which a failed submission could have left behind, and starts the next queued run when a finish notification was lost
// Submitting is idempotent per run, so a run that is already waiting in the pool is not enqueued twice
func (a *jobActor) resume(ctx context.Context, job jobsdb.Job, state *actorState, waiting []string) error {
	for _, id := range waiting {
		err := a.m.deps.Runs.Submit(ctx, id)
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
		return a.m.deps.Runs.Submit(ctx, next)
	}
	return nil
}

func (a *jobActor) save(ctx context.Context, state actorState) error {
	return a.client.SetState(ctx, state, nil)
}

func (a *jobActor) trigger(ctx context.Context, req runs.TriggerRequest) (runs.TriggerResult, error) {
	job, state, waiting, err := a.load(ctx)
	if database.IsNotFound(err) || (err == nil && job.ArchivedAt != nil) {
		return runs.TriggerResult{}, apperror.NotFound("Job")
	} else if err != nil {
		return runs.TriggerResult{}, err
	}

	// Repair a stuck slot first, so the decision below sees what is really running
	err = a.resume(ctx, job, &state, waiting)
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
		id, err := a.m.deps.Runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		return runs.TriggerResult{RunID: id, Status: runner.StatusSkipped}, nil

	case busy && job.Concurrency == ConcurrencyQueue:
		id, err := a.m.deps.Runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		state.Queue = append(state.Queue, id)
		return runs.TriggerResult{RunID: id, Status: runner.StatusQueued}, a.save(ctx, state)

	default:
		id, err := a.m.deps.Runs.Create(ctx, newRun)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		// State is saved before submitting, so a crash in between leaves a run the reconciler treats as active rather than a lost slot
		state.Active = append(state.Active, id)
		err = a.save(ctx, state)
		if err != nil {
			return runs.TriggerResult{}, err
		}
		return runs.TriggerResult{RunID: id, Status: runner.StatusQueued}, a.m.deps.Runs.Submit(ctx, id)
	}
}

// runFinished releases the concurrency slot and starts the next queued run
// The finished run is already terminal in the runs table, so reconciling the state drops it
func (a *jobActor) runFinished(ctx context.Context) error {
	job, state, waiting, err := a.load(ctx)
	if err != nil {
		return err
	}
	err = a.resume(ctx, job, &state, waiting)
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

	// The new alarm is armed before the pending one is dropped, so a failure in between leaves the job with its old schedule rather than none
	previous := state.Alarm
	state.Alarm = ""
	var nextAt *int64
	if job.ArchivedAt == nil && deref(job.Cron) != "" {
		next, err := nextRun(*job.Cron, deref(job.Timezone), time.Now())
		if err != nil {
			// The schedule was validated when it was saved, so a failure here is unexpected and leaves the job unscheduled until it is saved again
			slog.ErrorContext(ctx, "Job has an invalid schedule and stays unscheduled", slog.String("job", job.ID), slog.Any("error", err))
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

	// An alarm for the same occurrence has the same name and was just replaced, so only an alarm for another occurrence is dropped
	// An alarm that survives a failed delete is ignored when it fires, since it is no longer the pending one
	if previous != "" && previous != state.Alarm {
		_ = a.client.DeleteAlarm(ctx, previous)
	}

	// The next run time only feeds the UI, so failing to store it must not undo the armed schedule
	err = a.m.queries.SetNextRunAt(ctx, jobsdb.SetNextRunAtParams{WorkspaceID: job.WorkspaceID, ID: job.ID, NextRunAt: nextAt})
	if err != nil {
		slog.WarnContext(ctx, "Failed to store the next run time", slog.String("job", job.ID), slog.Any("error", err))
	}
	return nil
}

// forget drops the schedule alarm and the actor's state, for a job whose workspace is being deleted
// Neither goes away with the job's row, and reschedule can't find the alarm once the row is gone
func (a *jobActor) forget(ctx context.Context) error {
	state, err := a.client.GetState(ctx)
	if err != nil {
		return err
	}

	// An alarm that survives a failed delete is ignored when it fires, since the state no longer names it as pending
	if state.Alarm != "" {
		_ = a.client.DeleteAlarm(ctx, state.Alarm)
	}
	return a.client.DeleteState(ctx)
}

// fireSchedule runs the job for the occurrence the named alarm stands for and arms the next one
// Only the pending alarm triggers a run, and an empty name, used by the e2e helper, fires the schedule regardless
// A missed occurrence (e.g. during downtime) fires once when the alarm is delivered, and is never replayed multiple times
func (a *jobActor) fireSchedule(ctx context.Context, alarm string) error {
	state, err := a.client.GetState(ctx)
	if err != nil {
		return err
	}

	// The occurrence is forgotten before the run is triggered, so a redelivery after a crash, or a stale alarm a failed delete left behind, never runs the job twice
	if alarm != "" && alarm != state.Alarm {
		slog.WarnContext(ctx, "Ignoring an alarm that is not the pending schedule", slog.String("job", a.id), slog.String("alarm", alarm))
	} else {
		state.Alarm = ""
		err = a.save(ctx, state)
		if err != nil {
			return err
		}

		res, err := a.trigger(ctx, runs.TriggerRequest{Trigger: runs.TriggerSchedule})
		switch {
		case apperror.IsCode(err, apperror.CodeNotFound):
		case err != nil:
			slog.ErrorContext(ctx, "Scheduled run failed to start", slog.String("job", a.id), slog.Any("error", err))
		default:
			slog.InfoContext(ctx, "Scheduled run triggered", slog.String("job", a.id), slog.String("run", res.RunID), slog.String("status", res.Status))
		}
	}

	// The error makes Francis redeliver the alarm, since nothing else would arm the next occurrence
	// The occurrence was already forgotten above, so a redelivery only retries this step and never runs the job twice
	err = a.reschedule(ctx)
	if err != nil {
		return fmt.Errorf("failed to arm the next scheduled run: %w", err)
	}
	return nil
}

// placementRetryBackoff spans a replica restart, during which the cluster can briefly route an actor to a host that no longer owns it
var placementRetryBackoff = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

// invoke calls a method on a job actor, wherever it is placed in the cluster
func (m *Module) invoke(ctx context.Context, jobID, method string, data any, out any) error {
	for attempt := 0; ; attempt++ {
		env, err := m.deps.Actors.Service().Invoke(ctx, actorType, jobID, method, data)

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
