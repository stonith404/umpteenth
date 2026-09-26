//go:build unit

package stats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// seedRun inserts a finished run with the given status, duration and cost
func seedRun(t *testing.T, db *database.DB, wid, jobID string, number int64, status string, queuedAt int64, ms *int64, cost int64) {
	t.Helper()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, ms_total, cost, reflection_cost)
		VALUES ($1, $2, $3, $4, $5, 'explore', 'manual', 0, $6, $7, $8, 5)`,
		database.NewID(), wid, jobID, number, status, queuedAt, ms, cost)
}

func TestTotalsAggregatesInTheDatabase(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db})

	// Ten timed runs of 100..1000 ms plus runs that must not count toward durations or the period
	for i := int64(1); i <= 10; i++ {
		status := "succeeded"
		if i > 8 {
			status = "failed"
		}
		ms := i * 100
		seedRun(t, db, wid, job, i, status, 1000+i, &ms, 10)
	}
	seedRun(t, db, wid, job, 11, "timed_out", 1011, nil, 10)
	seedRun(t, db, wid, job, 12, "skipped", 1012, nil, 0)
	seedRun(t, db, wid, job, 13, "succeeded", 5000, nil, 10)

	totals, err := m.totals(context.Background(), wid, 1000, 2000)
	require.NoError(t, err)
	require.Equal(t, int64(11), totals.Runs)
	require.Equal(t, int64(8), totals.Succeeded)
	require.Equal(t, int64(3), totals.Failed)
	require.InDelta(t, 8.0/11.0, totals.SuccessRate, 0.0001)
	require.Equal(t, int64(500), totals.P50Ms)
	require.Equal(t, int64(900), totals.P95Ms)
	require.Equal(t, int64(110), totals.RunCost)
	require.Equal(t, int64(55), totals.LearningCost)

	// An empty period has no percentiles and no success rate
	empty, err := m.totals(context.Background(), wid, 9000, 10000)
	require.NoError(t, err)
	require.Equal(t, Totals{}, empty)
}

func TestGettingCheaperComparesFirstAndLastRuns(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	cheaper := testutil.SeedJob(t, db, wid, "skip")
	pricier := testutil.SeedJob(t, db, wid, "skip")
	short := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db})

	// The first three runs average 1000 and the last three average 250, with middle runs that must be ignored
	for i, cost := range []int64{900, 1000, 1100, 5000, 5000, 200, 250, 300} {
		seedRun(t, db, wid, cheaper, int64(i+1), "succeeded", int64(100+i), nil, cost)
	}
	seedRun(t, db, wid, cheaper, 99, "failed", 150, nil, 1)
	for i, cost := range []int64{100, 100, 100, 200, 200, 200} {
		seedRun(t, db, wid, pricier, int64(i+1), "succeeded", int64(100+i), nil, cost)
	}
	// Fewer than six runs would compare overlapping runs, so the job is left out
	for i, cost := range []int64{1000, 1000, 1000, 10, 10} {
		seedRun(t, db, wid, short, int64(i+1), "succeeded", int64(100+i), nil, cost)
	}

	out, err := m.gettingCheaper(context.Background(), wid, 0)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, cheaper, out[0].JobID)
	require.Equal(t, int64(1000), out[0].FirstCost)
	require.Equal(t, int64(250), out[0].RecentCost)
	require.InDelta(t, 0.75, out[0].DropPct, 0.0001)
	require.Equal(t, 8, out[0].Runs)
}

// allowJobs is a job checker that accepts every job
type allowJobs struct{}

func (allowJobs) JobExists(context.Context, string, string) error { return nil }

func TestJobStatsSummarizeTheWholeRangeAndChartTheNewestRuns(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db, Jobs: allowJobs{}})

	// The older half of the runs failed expensively and the newer half succeeded cheaply, so a truncated window would skew every KPI
	base := time.Now().Add(-24 * time.Hour).UnixMilli()
	fast, slow := int64(50), int64(200)
	for i := int64(1); i <= 600; i++ {
		if i <= 300 {
			seedRun(t, db, wid, job, i, "failed", base+i, &fast, 1000)
		} else {
			seedRun(t, db, wid, job, i, "succeeded", base+i, &slow, 10)
		}
	}
	seedRun(t, db, wid, job, 601, "skipped", base+601, nil, 0)

	ctx := principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid})
	out, err := m.jobStats(ctx, &jobStatsInput{ID: job, Range: "30d"})
	require.NoError(t, err)

	// Every run of the range counts, with the reflection cost of 5 per run included in the average
	require.InDelta(t, 0.5, out.Body.SuccessRate, 0.0001)
	require.Equal(t, int64((300*1005+300*15)/600), out.Body.AvgCost)
	require.Equal(t, int64(50), out.Body.P50Ms)

	// The chart holds the newest runs in chronological order and leaves skipped runs out
	require.Len(t, out.Body.Runs, maxJobChartRuns)
	require.Equal(t, int64(101), out.Body.Runs[0].Number)
	require.Equal(t, int64(600), out.Body.Runs[len(out.Body.Runs)-1].Number)
	for i := 1; i < len(out.Body.Runs); i++ {
		require.Less(t, out.Body.Runs[i-1].QueuedAt, out.Body.Runs[i].QueuedAt)
	}
}

func TestPerDayStartsAtTheBeginningOfTheFirstDay(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db})

	// A run early on the first day but before from still belongs to that day's bucket
	// Setting the lowest bit keeps from off midnight, which is always an even number of milliseconds
	from := time.Now().Add(-48*time.Hour).UnixMilli() | 1
	first := dayStart(from)
	seedRun(t, db, wid, job, 1, "succeeded", first, nil, 10)
	seedRun(t, db, wid, job, 2, "failed", from, nil, 20)
	seedRun(t, db, wid, job, 3, "succeeded", first-1, nil, 40)

	days, err := m.perDay(context.Background(), wid, from)
	require.NoError(t, err)
	require.Equal(t, first, days[0].Day)
	require.Equal(t, int64(1), days[0].Succeeded)
	require.Equal(t, int64(1), days[0].Failed)
	require.Equal(t, int64(10+20+2*5), days[0].Cost)

	costs, err := m.costByJob(context.Background(), wid, from)
	require.NoError(t, err)
	require.Len(t, costs, 1)
	require.Equal(t, first, costs[0].Day)
	require.Equal(t, int64(10+20+2*5), costs[0].Cost)
}
