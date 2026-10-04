//go:build unit

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/italypaleale/francis/actor"
	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
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
	// calls counts every Submit, including repeated submissions of the same run
	calls int
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
	q.calls++
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

	first, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, first.Status)

	// A second trigger while the first run is active is recorded as skipped and never submitted
	second, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerWebhook})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, second.Status)
	require.Equal(t, []string{first.RunID}, queue.Submitted())

	// Once the first run finished, the job accepts triggers again
	finishRun(t, db, first.RunID)
	m.RunFinished(ctx, runner.Run{ID: first.RunID, JobID: jobID}, runner.Final{})
	third, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, third.Status)
	require.Equal(t, []string{first.RunID, third.RunID}, queue.Submitted())
}

func TestQueuePolicyStartsTheNextRunWhenTheActiveOneFinishes(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
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
	_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.Error(t, err)
	require.Empty(t, queue.Submitted())

	// The next trigger is skipped because that run still holds the slot, but it resubmits the run instead of leaving the job stuck
	second, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerSchedule})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, second.Status)
	require.Len(t, queue.Submitted(), 1)
}

func TestFailedSubmissionIsRetriedWithoutAnotherTrigger(t *testing.T) {
	delay := resumeRetryDelay
	resumeRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { resumeRetryDelay = delay })
	m, queue, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	// The task pool stays unreachable for the trigger and the first two retries
	queue.failNext = 3
	_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.Error(t, err)
	require.Empty(t, queue.Submitted())

	// The run still starts once the task pool is back, although nothing triggers the job again
	require.Eventually(t, func() bool { return len(queue.Submitted()) == 1 }, 20*time.Second, 50*time.Millisecond)
	var runID string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM runs WHERE job_id = $1", jobID).Scan(&runID))
	require.Equal(t, []string{runID}, queue.Submitted())
}

func TestQueueRecoversFromALostFinishNotification(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)

	// The first run finishes but its replica dies before the actor hears about it
	finishRun(t, db, a.RunID)

	// The next trigger starts the waiting run first and queues itself behind it, keeping the order of triggers
	c, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, c.Status)
	require.Equal(t, []string{a.RunID, b.RunID}, queue.Submitted())
}

func TestArchivingCancelsQueuedRuns(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)
	ctx := principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid})

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	b, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
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
		_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
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
	res, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerSchedule})
	require.NoError(t, err)
	require.Equal(t, runner.StatusSkipped, res.Status)
	require.Empty(t, queue.Submitted())
}

func TestQueuePolicyQueuesARunTheStateMissed(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	a, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)

	// A trigger created a run behind the active one, but saving the actor state failed before it was queued
	testutil.Exec(t, db, "UPDATE jobs SET run_counter = run_counter + 1 WHERE id = $1", jobID)
	orphan := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 2, 'queued', 'explore', 'manual', 0, $4)`,
		orphan, wid, jobID, database.Now())

	// The next trigger queues that run instead of starting it next to the active one, and waits behind it
	c, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)
	require.Equal(t, runner.StatusQueued, c.Status)
	require.Equal(t, []string{a.RunID}, queue.Submitted())

	// The runs start one at a time, in the order they were triggered
	finishRun(t, db, a.RunID)
	m.RunFinished(ctx, runner.Run{ID: a.RunID, JobID: jobID}, runner.Final{})
	require.Equal(t, []string{a.RunID, orphan}, queue.Submitted())
	finishRun(t, db, orphan)
	m.RunFinished(ctx, runner.Run{ID: orphan, JobID: jobID}, runner.Final{})
	require.Equal(t, []string{a.RunID, orphan, c.RunID}, queue.Submitted())
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
	require.Equal(t, runs.TriggerSchedule, trigger)

	// Clearing the cron removes the schedule
	testutil.Exec(t, db, "UPDATE jobs SET cron = NULL WHERE id = $1", jobID)
	require.NoError(t, m.Reschedule(ctx, jobID))
	job, err = m.getJob(ctx, wid, jobID)
	require.NoError(t, err)
	require.Nil(t, job.NextRunAt)
}

func TestTriggerResubmitsEachWaitingRunOnce(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyParallel)

	// Three runs wait in the pool, which is full, so the next trigger resubmits each of them in case a submission was lost
	for range 3 {
		_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
		require.NoError(t, err)
	}
	queue.mu.Lock()
	queue.calls = 0
	queue.mu.Unlock()
	_, err := m.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
	require.NoError(t, err)

	// Waiting runs from earlier turns must not pile up in the actor's cached state and be submitted again and again
	queue.mu.Lock()
	defer queue.mu.Unlock()
	require.Equal(t, 4, queue.calls)
}

func TestScheduleAlarmFiresOncePerOccurrence(t *testing.T) {
	m, queue, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyParallel)
	testutil.Exec(t, db, "UPDATE jobs SET cron = '*/5 * * * *' WHERE id = $1", jobID)

	// A standalone actor instance receives alarms the way Francis delivers them, with the name of the alarm that fired
	a := m.newActor(jobID, m.deps.Actors.Service()).(*jobActor)
	require.NoError(t, a.save(ctx, actorState{Alarm: "schedule-100"}))

	// The pending alarm runs the job and arms the next occurrence
	require.NoError(t, a.Alarm(ctx, "schedule-100", nil))
	require.Len(t, queue.Submitted(), 1)
	state, err := a.client.GetState(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, state.Alarm)
	require.NotEqual(t, "schedule-100", state.Alarm)

	// A redelivery of the same occurrence after a crash, or a stale alarm a failed delete left behind, doesn't run it again
	require.NoError(t, a.Alarm(ctx, "schedule-100", nil))
	require.NoError(t, a.Alarm(ctx, "schedule-50", nil))
	require.Len(t, queue.Submitted(), 1)
}

// flakyJobReads passes every query through, but fails the job reads with the given numbers, like a connection that drops for a while
type flakyJobReads struct {
	jobsdb.DBTX
	mu     sync.Mutex
	reads  int
	failAt []int
}

func (f *flakyJobReads) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "name: GetJobUnscoped ") {
		f.mu.Lock()
		f.reads++
		fail := slices.Contains(f.failAt, f.reads)
		f.mu.Unlock()
		if fail {
			// A cancelled context makes the driver fail the read, standing in for a transient database error
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			return f.DBTX.QueryRowContext(cancelled, query, args...)
		}
	}
	return f.DBTX.QueryRowContext(ctx, query, args...)
}

func (f *flakyJobReads) Reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func TestScheduleSurvivesATransientErrorWhileRearming(t *testing.T) {
	delay := resumeRetryDelay
	resumeRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { resumeRetryDelay = delay })
	m, queue, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyParallel)
	testutil.Exec(t, db, "UPDATE jobs SET cron = '0 0 1 1 *' WHERE id = $1", jobID)

	// The first job read comes from the run trigger, and the next four, made while re-arming the schedule, fail, which is more attempts than Francis would make
	flaky := &flakyJobReads{DBTX: db, failAt: []int{2, 3, 4, 5}}
	m.queries = jobsdb.New(flaky)

	// Francis delivers an occurrence that is due now, and the one after it is too far away to fire during the test
	a := m.newActor(jobID, m.deps.Actors.Service()).(*jobActor)
	require.NoError(t, a.save(ctx, actorState{Alarm: "schedule-1"}))
	require.NoError(t, a.client.SetAlarm(ctx, "schedule-1", actor.AlarmProperties{DueTime: time.Now()}))

	// The job retries the failed turn until it re-arms the schedule, instead of leaving the job without an alarm
	// The state is read from the service, since the client above keeps its own copy
	require.Eventually(t, func() bool {
		var state actorState
		err := m.deps.Actors.Service().GetState(ctx, actorType, jobID, &state)
		return err == nil && state.Alarm != "" && state.Alarm != "schedule-1"
	}, 20*time.Second, 100*time.Millisecond)
	require.GreaterOrEqual(t, flaky.Reads(), 6)
	job, err := m.getJob(ctx, wid, jobID)
	require.NoError(t, err)
	require.NotNil(t, job.NextRunAt)
	require.True(t, time.UnixMilli(*job.NextRunAt).After(time.Now()))

	// The retry only re-arms the schedule, so the occurrence ran exactly once
	require.Len(t, queue.Submitted(), 1)
}

// alarmRecorder passes alarm calls through to the actor client and records deletes, and can fail arming like a provider that is briefly unreachable
type alarmRecorder struct {
	actor.Client[actorState]
	failSet bool
	deleted []string
}

func (r *alarmRecorder) SetAlarm(ctx context.Context, name string, props actor.AlarmProperties) error {
	if r.failSet {
		return errors.New("provider unavailable")
	}
	return r.Client.SetAlarm(ctx, name, props)
}

func (r *alarmRecorder) DeleteAlarm(ctx context.Context, name string) error {
	r.deleted = append(r.deleted, name)
	return r.Client.DeleteAlarm(ctx, name)
}

func TestRescheduleKeepsThePendingAlarmUntilTheNewOneIsArmed(t *testing.T) {
	m, _, db := newTestModule(t)
	ctx := t.Context()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	testutil.Exec(t, db, "UPDATE jobs SET cron = '0 0 1 1 *' WHERE id = $1", jobID)

	a := m.newActor(jobID, m.deps.Actors.Service()).(*jobActor)
	rec := &alarmRecorder{Client: a.client}
	a.client = rec
	pending := func() string {
		state, err := a.client.GetState(ctx)
		require.NoError(t, err)
		return state.Alarm
	}
	require.NoError(t, a.reschedule(ctx))
	first := pending()
	require.NotEmpty(t, first)

	// Saving the job without changing its schedule re-arms the same occurrence in place instead of deleting it
	require.NoError(t, a.reschedule(ctx))
	require.Equal(t, first, pending())
	require.Empty(t, rec.deleted)

	// A new schedule that can't be armed leaves the pending alarm in place, so the job keeps running on its old schedule
	testutil.Exec(t, db, "UPDATE jobs SET cron = '0 0 1 7 *' WHERE id = $1", jobID)
	rec.failSet = true
	require.Error(t, a.reschedule(ctx))
	require.Equal(t, first, pending())
	require.Empty(t, rec.deleted)

	// Once arming works, the new alarm replaces the pending one
	rec.failSet = false
	require.NoError(t, a.reschedule(ctx))
	require.NotEqual(t, first, pending())
	require.Equal(t, []string{first}, rec.deleted)
}

func TestUpdateDetectsAConcurrentWrite(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	before, err := m.getJob(ctx, wid, jobID)
	require.NoError(t, err)

	// A patch moves updated_at forward even within the millisecond the job was created in
	out, err := m.update(ctx, &updateInput{ID: jobID, Body: jobPatch{Name: new("Renamed")}})
	require.NoError(t, err)
	require.Equal(t, "Renamed", out.Body.Name)
	require.Greater(t, out.Body.UpdatedAt, before.UpdatedAt)

	// A write based on the job as it was before that patch is refused instead of reverting it
	n, err := m.queries.UpdateJob(ctx, jobsdb.UpdateJobParams{
		WorkspaceID: wid, ID: jobID, Name: before.Name, Instruction: before.Instruction, Spec: before.Spec, Network: before.Network,
		AllowedDomains: before.AllowedDomains, Limits: before.Limits, Concurrency: before.Concurrency,
		UpdatedAt: database.Now(), ReadUpdatedAt: before.UpdatedAt,
	})
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestWebhookAcceptsAnEmptyBodyAndChecksTheToken(t *testing.T) {
	m, queue, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyParallel)
	token, err := m.rotateWebhookToken(principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid}), &idInput{ID: jobID})
	require.NoError(t, err)
	_, api := humatest.New(t)
	m.RegisterRoutes(api, nil)

	// A wrong token never starts a run
	resp := api.Post("/hooks/"+jobID, "Authorization: Bearer umh_wrong")
	require.Equal(t, http.StatusUnauthorized, resp.Code)
	require.Empty(t, queue.Submitted())

	// Schedulers often call webhooks without a body, which the request check and the OpenAPI document both allow
	resp = api.Post("/hooks/"+jobID, "Authorization: Bearer "+token.Body.Token)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	require.Len(t, queue.Submitted(), 1)
	require.False(t, api.OpenAPI().Paths["/hooks/{jobId}"].Post.RequestBody.Required)
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
