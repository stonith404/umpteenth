//go:build unit

package runs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// countRows counts the rows of a table that belong to the run
func countRows(t *testing.T, db *database.DB, query, runID string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(t.Context(), query, runID).Scan(&n))
	return n
}

func TestDeleteRemovesAFinishedRunWithItsEventsAndFiles(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())
	runID := seedRun(t, db, wid, job, 1, runner.StatusSucceeded, nil, new(database.Now()))
	seedEvents(t, db, runID, 1, 5)
	require.NoError(t, m.deps.Storage.Save(t.Context(), "runs/"+runID+"/artifacts/report.md", strings.NewReader("# Report")))

	require.NoError(t, m.Delete(t.Context(), wid, runID))

	require.Zero(t, countRows(t, db, "SELECT COUNT(*) FROM runs WHERE id = $1", runID))
	require.Zero(t, countRows(t, db, "SELECT COUNT(*) FROM run_events WHERE run_id = $1", runID))
	files, err := m.deps.Storage.List(t.Context(), "runs/"+runID)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestDeleteRefusesRunsThatHaveNotFinished(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "queue")
	m := newModuleForTest(t, db, events.NewLocalBus())

	// A live run still has a runner writing to it, and a pending reflection still reads its events and adds its spend
	running := seedRun(t, db, wid, job, 1, runner.StatusRunning, new(database.Now()), nil)
	reflecting := seedRun(t, db, wid, job, 2, runner.StatusSucceeded, nil, new(database.Now()))
	testutil.Exec(t, db, "UPDATE runs SET reflection = 'pending' WHERE id = $1", reflecting)

	for _, id := range []string{running, reflecting} {
		err := m.Delete(t.Context(), wid, id)
		require.True(t, apperror.IsCode(err, apperror.CodeConflict), err)
		require.Equal(t, 1, countRows(t, db, "SELECT COUNT(*) FROM runs WHERE id = $1", id))
	}
}

func TestDeleteOnlyReachesRunsOfTheWorkspace(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	other := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, other, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())
	runID := seedRun(t, db, other, job, 1, runner.StatusFailed, nil, new(database.Now()))

	err := m.Delete(t.Context(), wid, runID)
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound), err)
	require.Equal(t, 1, countRows(t, db, "SELECT COUNT(*) FROM runs WHERE id = $1", runID))

	deleted, skipped, err := m.DeleteMany(t.Context(), wid, []string{runID})
	require.NoError(t, err)
	require.Empty(t, deleted)
	require.Equal(t, []string{runID}, skipped)
}

func TestDeleteManySkipsRunsItCannotDelete(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "queue")
	bus := events.NewLocalBus()
	m := newModuleForTest(t, db, bus)
	succeeded := seedRun(t, db, wid, job, 1, runner.StatusSucceeded, nil, new(database.Now()))
	cancelled := seedRun(t, db, wid, job, 2, runner.StatusCancelled, nil, new(database.Now()))
	queued := seedRun(t, db, wid, job, 3, runner.StatusQueued, nil, nil)

	// Open tables learn about each deleted run
	msgs, unsubscribe := bus.Subscribe(events.WorkspaceTopic(wid))
	defer unsubscribe()

	deleted, skipped, err := m.DeleteMany(t.Context(), wid, []string{succeeded, queued, "unknown", cancelled})
	require.NoError(t, err)
	require.Equal(t, []string{succeeded, cancelled}, deleted)
	require.Equal(t, []string{queued, "unknown"}, skipped)
	require.Equal(t, 1, countRows(t, db, "SELECT COUNT(*) FROM runs WHERE id = $1", queued))

	for _, id := range deleted {
		msg := <-msgs
		require.Contains(t, string(msg), `"deleted":true`)
		require.Contains(t, string(msg), id)
	}
}
