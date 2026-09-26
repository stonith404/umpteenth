//go:build unit

package reflection

import (
	"database/sql"
	"testing"
	"time"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// hostedModule builds the module on a real actor host, so reflections go through the taskpool like in production
func hostedModule(t *testing.T, db *database.DB, pb *playbook.Module, provider *fake.Provider) *Module {
	t.Helper()
	var m *Module
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		var err error
		m, err = New(Dependencies{
			DB: db, Actors: h, Bus: events.NewLocalBus(), Storage: storage.NewDatabaseStorage(db), Playbook: pb, Settings: fakeSettings{},
			Models: fakeModels{provider}, Jobs: fakeJobs{runner.JobConfig{Instruction: "Summarize the top stories", BaseImage: "sandbox:latest", ModelID: "m1"}},
			Demotion: notDemoted{}, MaintenanceDisabled: true,
		})
		require.NoError(t, err)
	})
	return m
}

type hostedRun struct{ wid, jobID, runID string }

// finishRun seeds a run in its own workspace that finished at the given time, when the runs store marked its reflection pending, and hands it to reflection like the runner does
func finishRun(t *testing.T, db *database.DB, m *Module, finishedAt time.Time) hostedRun {
	t.Helper()
	r := hostedRun{wid: testutil.SeedWorkspace(t, db)}
	r.jobID = testutil.SeedJob(t, db, r.wid, "skip")
	r.runID = database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, finished_at, turns, reflection, reflection_requested_at)
		VALUES ($1, $2, $3, 1, 'succeeded', 'assisted', 'manual', 0, $4, $4, 3, 'pending', $4)`, r.runID, r.wid, r.jobID, finishedAt.UnixMilli())
	m.RunFinished(t.Context(), runner.Run{ID: r.runID, WorkspaceID: r.wid, JobID: r.jobID, Number: 1, Status: runner.StatusSucceeded, Mode: runner.ModeAssisted},
		runner.Final{Status: runner.StatusSucceeded, Turns: 3, Reflection: runner.ReflectionPending, SelfImprove: true})
	return r
}

type reflectionState struct {
	Reflection string
	Error      sql.NullString
	Version    sql.NullInt64
	Cost       int64
	// Written is the playbook version reflection wrote from the run, whether or not the run recorded it
	Written sql.NullInt64
}

func loadReflection(t *testing.T, db *database.DB, r hostedRun) reflectionState {
	t.Helper()
	var s reflectionState
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT reflection, reflection_error, reflection_version, reflection_cost FROM runs WHERE id = $1`, r.runID).Scan(&s.Reflection, &s.Error, &s.Version, &s.Cost))
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM playbook_versions WHERE job_id = $1 AND source_run_id = $2`, r.jobID, r.runID).Scan(&s.Written))
	return s
}

// learnedAnswer is a reflection answer that adds a learning, so every reflection writes a version
var learnedAnswer = map[string]any{"summary": "Learned something", "usedLearnings": []string{}, "ops": []any{
	op(playbook.OpAddLearning, map[string]any{"kind": "fact", "text": "The API pages at 100"}),
}}

// TestReconcilerKeepsReflectionsWhoseTaskIsAlive checks that a reflection that is merely waiting for the replica's one slot, or still running, is not failed as lost
func TestReconcilerKeepsReflectionsWhoseTaskIsAlive(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	pb := playbook.New(playbook.Dependencies{DB: db})

	// The first answer keeps its task busy for a while, so the other reflection has to wait for the slot
	provider := fake.New()
	slow := submit(t, learnedAnswer)
	slow.Delay = 3 * time.Second
	provider.Enqueue(slow, submit(t, learnedAnswer))
	m := hostedModule(t, db, pb, provider)

	// Two runs in different workspaces finished together half an hour ago, like jobs on a common schedule do
	finishedAt := time.Now().Add(-31 * time.Minute)
	runs := []hostedRun{finishRun(t, db, m, finishedAt), finishRun(t, db, m, finishedAt)}

	// One reflection is talking to the model while the other one waits for the replica's only slot
	require.Eventually(t, func() bool { return len(provider.Requests()) == 1 }, 10*time.Second, 20*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, 1, len(provider.Requests()), "the second reflection must be queued behind the first")

	// The reconciler fires now, while neither task was lost
	require.NoError(t, m.reconcile(ctx))

	// Give the running reflection time to finish and the queued one time to run after it
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if loadReflection(t, db, runs[0]).Reflection == StatusDone && loadReflection(t, db, runs[1]).Reflection == StatusDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Both runs were learned from, and each run records the playbook version its reflection wrote
	for i, r := range runs {
		s := loadReflection(t, db, r)
		assert.Equal(t, StatusDone, s.Reflection, "run %d", i)
		assert.False(t, s.Error.Valid, "run %d has error %q", i, s.Error.String)
		assert.Equal(t, s.Written, s.Version, "run %d must record the playbook version its reflection wrote", i)
		assert.True(t, s.Version.Valid, "run %d has no playbook version", i)
	}
	assert.Equal(t, 2, len(provider.Requests()), "each run must have been reflected on by the model")
}

// TestStartedReflectionIsNotStaleAndSavesOnlyWhileClaimed checks that starting a reflection restarts the reconciler's clock, and that a reflection that lost its claim writes no version
func TestStartedReflectionIsNotStaleAndSavesOnlyWhileClaimed(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	slow := submit(t, learnedAnswer)
	slow.Delay = 3 * time.Second
	provider.Enqueue(slow)
	m := hostedModule(t, db, pb, provider)

	// The reflection was requested longer ago than the reconciler waits, and only now starts talking to the model
	r := finishRun(t, db, m, time.Now().Add(-staleAfter-time.Minute))
	require.Eventually(t, func() bool { return len(provider.Requests()) == 1 }, 10*time.Second, 20*time.Millisecond)

	// The reconciler fires meanwhile and leaves the started reflection alone
	require.NoError(t, m.reconcile(ctx))
	require.Equal(t, StatusPending, loadReflection(t, db, r).Reflection)

	// Another task claims the run before the answer arrives, like a retry on another replica does
	testutil.Exec(t, db, `UPDATE runs SET reflection_requested_at = reflection_requested_at + 1 WHERE id = $1`, r.runID)

	// The reflection still counts what it spent, but writes no version and leaves the result to the task that took over
	require.Eventually(t, func() bool { return loadReflection(t, db, r).Cost > 0 }, 10*time.Second, 20*time.Millisecond)
	s := loadReflection(t, db, r)
	assert.Equal(t, StatusPending, s.Reflection)
	assert.False(t, s.Written.Valid, "reflection wrote version %d although its run can't record it", s.Written.Int64)
}
