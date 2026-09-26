//go:build unit

package jobs

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// fakeQueue inserts real run rows, because the actor reconciles against the runs table, and records submissions
type fakeQueue struct {
	db        *database.DB
	mu        sync.Mutex
	submitted []string
	// failNext makes that many Submit calls fail, like a task pool that is briefly unreachable
	failNext int
}

func (q *fakeQueue) Create(ctx context.Context, n runs.NewRun) (string, error) {
	id := database.NewID()
	_, err := q.db.ExecContext(ctx, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, n.WorkspaceID, n.JobID, n.Number, n.Status, n.Mode, n.Trigger, n.PlaybookVersion, database.Now())
	return id, err
}

func (q *fakeQueue) Cancel(ctx context.Context, workspaceID, runID string) error {
	_, err := q.db.ExecContext(ctx, "UPDATE runs SET status = 'cancelled', finished_at = $1 WHERE workspace_id = $2 AND id = $3 AND status = 'queued'", database.Now(), workspaceID, runID)
	return err
}

// Submit is idempotent per run like the real task pool, which dedups on the run ID as task key
func (q *fakeQueue) Submit(_ context.Context, runID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failNext > 0 {
		q.failNext--
		return errors.New("task pool unavailable")
	}
	if !slices.Contains(q.submitted, runID) {
		q.submitted = append(q.submitted, runID)
	}
	return nil
}

func (q *fakeQueue) Submitted() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.submitted...)
}

func newTestModule(t *testing.T) (*Module, *fakeQueue, *database.DB) {
	db := testutil.NewDatabaseForTest(t)
	queue := &fakeQueue{db: db}
	var m *Module
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		var err error
		m, err = New(Dependencies{
			DB:        db,
			Actors:    h,
			Runs:      queue,
			Playbooks: playbook.New(playbook.Dependencies{DB: db}),
			Settings:  settings.New(settings.Dependencies{DB: db, Defaults: settings.Defaults{Image: "img", RetentionDays: 90}}),
		})
		require.NoError(t, err)
	})
	return m, queue, db
}

func finishRun(t *testing.T, db *database.DB, runID string) {
	testutil.Exec(t, db, "UPDATE runs SET status = 'succeeded', finished_at = $1 WHERE id = $2", database.Now(), runID)
}

func TestSkipPolicyRecordsSkippedRuns(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)

	first, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, first.Status)

	// A second trigger while the first run is active is recorded as skipped and never submitted
	second, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerWebhook})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, second.Status)
	require.Equal(t, []string{first.RunID}, queue.Submitted())

	// Once the first run finished, the job accepts triggers again
	finishRun(t, db, first.RunID)
	m.RunFinished(ctx, runner.Run{ID: first.RunID, JobID: jobID}, runner.Final{})
	third, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, third.Status)
	require.Equal(t, []string{first.RunID, third.RunID}, queue.Submitted())
}

func TestQueuePolicyStartsTheNextRunWhenTheActiveOneFinishes(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, b.Status)
	require.Equal(t, []string{a.RunID}, queue.Submitted(), "the second run waits")

	finishRun(t, db, a.RunID)
	m.RunFinished(ctx, runner.Run{ID: a.RunID, JobID: jobID}, runner.Final{})
	require.Equal(t, []string{a.RunID, b.RunID}, queue.Submitted())
}

func TestFailedSubmissionIsRetriedByTheNextTurn(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)

	// The task pool is unreachable when the first run is submitted
	queue.failNext = 1
	_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.Error(t, err)
	require.Empty(t, queue.Submitted())

	// The next trigger is skipped because that run still holds the slot, but it resubmits the run instead of leaving the job stuck
	second, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerSchedule})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, second.Status)
	require.Len(t, queue.Submitted(), 1)
}

func TestQueueRecoversFromALostFinishNotification(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)

	// The first run finishes but its replica dies before the actor hears about it
	finishRun(t, db, a.RunID)

	// The next trigger starts the waiting run first and queues itself behind it, keeping the order of triggers
	c, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, c.Status)
	require.Equal(t, []string{a.RunID, b.RunID}, queue.Submitted())
}

func TestArchivingCancelsQueuedRuns(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)
	ctx := principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid})

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
	require.NoError(t, err)

	// The waiting run can never start once the job is archived, so it is cancelled with it
	_, err = m.archive(ctx, &idInput{ID: jobID})
	require.NoError(t, err)
	var status string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = $1", b.RunID).Scan(&status))
	require.Equal(t, runner.StatusCancelled, status)
	require.NotEqual(t, a.RunID, b.RunID)
}

func TestParallelPolicySubmitsEveryRun(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyParallel)

	for range 3 {
		_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerManual})
		require.NoError(t, err)
	}
	require.Len(t, queue.Submitted(), 3)
}

func TestLostActorStateStillPreventsDoubleRuns(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)

	// A live run the actor has never heard of, e.g. after its state was lost, still counts as active
	testutil.Exec(t, db, "UPDATE jobs SET run_counter = 1 WHERE id = $1", jobID)
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'explore', 'manual', 0, $4)`,
		database.NewID(), wid, jobID, database.Now())
	res, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: TriggerSchedule})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, res.Status)
	require.Empty(t, queue.Submitted())
}

func TestScheduleArmsAlarmAndFiresRuns(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	testutil.Exec(t, db, "UPDATE jobs SET cron = '*/5 * * * *', timezone = 'Europe/Berlin' WHERE id = $1", jobID)

	require.NoError(t, m.Reschedule(ctx, jobID))
	job, err := m.getJob(ctx, wid, jobID)
	require.NoError(t, err)
	require.NotNil(t, job.NextRunAt)
	next := time.UnixMilli(*job.NextRunAt)
	require.WithinDuration(t, time.Now(), next, 5*time.Minute+time.Second)
	require.Zero(t, next.Minute()%5)

	// Firing the schedule creates a run with the schedule trigger and re-arms the alarm
	require.NoError(t, m.invoke(ctx, jobID, methodFireSchedule, nil, nil))
	require.Len(t, queue.Submitted(), 1)
	var trigger string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT trigger FROM runs WHERE id = $1", queue.Submitted()[0]).Scan(&trigger))
	require.Equal(t, TriggerSchedule, trigger)

	// Disabling the job removes the schedule
	testutil.Exec(t, db, "UPDATE jobs SET enabled = FALSE WHERE id = $1", jobID)
	require.NoError(t, m.Reschedule(ctx, jobID))
	job, err = m.getJob(ctx, wid, jobID)
	require.NoError(t, err)
	require.Nil(t, job.NextRunAt)
}

func TestNextRunRespectsTimezone(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	after := time.Date(2026, 9, 25, 7, 30, 0, 0, berlin)
	next, err := nextRun("0 8 * * 1-5", "Europe/Berlin", after)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 25, 8, 0, 0, 0, berlin).Unix(), next.Unix())

	// Friday after 08:00 rolls over to Monday
	next, err = nextRun("0 8 * * 1-5", "Europe/Berlin", time.Date(2026, 9, 25, 9, 0, 0, 0, berlin))
	require.NoError(t, err)
	require.Equal(t, time.Monday, next.In(berlin).Weekday())

	_, err = nextRun("not a cron", "", after)
	require.Error(t, err)
	_, err = nextRun("* * * * *", "Mars/Olympus", after)
	require.Error(t, err)
}
