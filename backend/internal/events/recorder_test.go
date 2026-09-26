//go:build unit

package events

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestConcurrentEventsAreAllRecorded(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'explore', 'manual', 0, $4)`,
		runID, wid, jobID, database.Now())
	rec := NewRecorder(db, NewLocalBus(), storage.NewDatabaseStorage(db), runID)

	// Parallel tool calls and broker requests emit at the same time
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() { rec.Emit(context.Background(), Event{Type: TypeLog, Payload: map[string]any{"message": "x"}}) })
	}
	wg.Wait()

	var count int64
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM run_events WHERE run_id = $1`, runID).Scan(&count))
	require.EqualValues(t, 30, count)
}
