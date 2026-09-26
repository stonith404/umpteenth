//go:build unit

package runs

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// heartbeatTimeout matches the per-attempt timeout of the Francis health check, whose failure stops the whole server
const heartbeatTimeout = 2500 * time.Millisecond

func TestPruneOfALargeWindowDoesNotStarveOtherWriters(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 800,000 events")
	}
	ctx := context.Background()

	// A file database behaves like production, with WAL, one writer at a time and the busy timeout
	db, err := database.Open(ctx, database.EngineSQLite, filepath.Join(t.TempDir(), "prune.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.Migrate(ctx, db))

	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())

	// Lowering the retention opens a window of weeks of finished runs, each with a timeline of a hundred events
	const runCount, eventsPerRun = 8000, 100
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	first := cutoff.Add(-60 * 24 * time.Hour).UnixMilli()
	step := (cutoff.UnixMilli() - first) / runCount
	testutil.Exec(t, db, `WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < $1)
		INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, finished_at)
		SELECT printf('run-%06d', n), $2, $3, n, $4, 'explore', 'schedule', 0, $5 + (n - 1) * $6, $5 + (n - 1) * $6 FROM s`,
		runCount, wid, job, runner.StatusSucceeded, first, step)
	testutil.Exec(t, db, `WITH RECURSIVE e(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM e WHERE n < $1)
		INSERT INTO run_events (run_id, seq, ts, type, payload)
		SELECT r.id, e.n, r.finished_at, 'tool_call', '{"output":"' || hex(randomblob(200)) || '"}' FROM runs r, e ORDER BY r.id, e.n`,
		eventsPerRun)

	// Last night's prune ran with the old retention of 90 days
	testutil.Exec(t, db, "INSERT INTO kv (key, value) VALUES ($1, $2)", "retention-watermark/"+wid, strconv.FormatInt(first, 10))

	// A heartbeat writes throughout the prune with the same deadline the actor host gives its health check
	done := make(chan struct{})
	var wg sync.WaitGroup
	var failures []string
	var slowest time.Duration
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(50 * time.Millisecond):
			}
			attemptCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
			start := time.Now()
			_, err := db.ExecContext(attemptCtx, "INSERT INTO kv (key, value) VALUES ('heartbeat', $1) ON CONFLICT (key) DO UPDATE SET value = excluded.value", start.String())
			cancel()
			slowest = max(slowest, time.Since(start))
			if err != nil {
				failures = append(failures, time.Since(start).Round(time.Millisecond).String()+": "+err.Error())
			}
		}
	})

	start := time.Now()
	m.pruneWorkspace(ctx, wid, cutoff)
	took := time.Since(start)
	close(done)
	wg.Wait()
	t.Logf("prune took %s, slowest heartbeat %s", took.Round(time.Millisecond), slowest.Round(time.Millisecond))

	// The prune still removes every event in the window
	var left int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_events").Scan(&left))
	require.Zero(t, left)

	// No other writer may wait out its deadline while the prune holds the write lock
	require.Empty(t, failures, "writes failed while the prune ran")
	require.Less(t, slowest, heartbeatTimeout)
}
