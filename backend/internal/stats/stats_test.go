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

// seedRun inserts a finished run with the given status, duration and cost, which used three tokens per micro-USD
func seedRun(t *testing.T, db *database.DB, wid, jobID string, number int64, status string, queuedAt int64, ms *int64, cost int64) {
	t.Helper()
	seedRunUsage(t, db, wid, jobID, number, status, queuedAt, ms, cost, 3*cost)
}

// seedRunUsage inserts a finished run with the given cost and input plus output tokens, and a reflection that cost 5 and used 50 tokens
func seedRunUsage(t *testing.T, db *database.DB, wid, jobID string, number int64, status string, queuedAt int64, ms *int64, cost, tokens int64) {
	t.Helper()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, ms_total, cost, tok_in, tok_out, reflection_cost, reflection_tokens)
		VALUES ($1, $2, $3, $4, $5, 'explore', 'manual', 0, $6, $7, $8, $9, $10, 5, 50)`,
		database.NewID(), wid, jobID, number, status, queuedAt, ms, cost, tokens-tokens/3, tokens/3)
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

	totals, err := m.totals(t.Context(), wid, 1000, 2000)
	require.NoError(t, err)
	require.Equal(t, int64(11), totals.Runs)
	require.Equal(t, int64(8), totals.Succeeded)
	require.Equal(t, int64(3), totals.Failed)
	require.InDelta(t, 8.0/11.0, totals.SuccessRate, 0.0001)
	require.Equal(t, int64(500), totals.P50Ms)
	require.Equal(t, int64(900), totals.P95Ms)
	require.Equal(t, int64(110), totals.RunCost)
	require.Equal(t, int64(55), totals.LearningCost)
	require.Equal(t, int64(330), totals.RunTokens)
	require.Equal(t, int64(550), totals.LearningTokens)

	// An empty period has no percentiles and no success rate
	empty, err := m.totals(t.Context(), wid, 9000, 10000)
	require.NoError(t, err)
	require.Equal(t, Totals{}, empty)
}

func TestGettingCheaperComparesFirstAndLastRuns(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	cheaper := testutil.SeedJob(t, db, wid, "skip")
	pricier := testutil.SeedJob(t, db, wid, "skip")
	short := testutil.SeedJob(t, db, wid, "skip")
	switched := testutil.SeedJob(t, db, wid, "skip")
	trimmed := testutil.SeedJob(t, db, wid, "skip")
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

	// A switch to a cheaper model that needs more tokens gets cheaper without getting leaner
	for i, cost := range []int64{300, 300, 300, 100, 100, 100} {
		seedRunUsage(t, db, wid, switched, int64(i+1), "succeeded", int64(100+i), nil, cost, 1000*(1+int64(i)/3))
	}
	// Trimmed prompts on a pricier model get leaner without getting cheaper
	for i, cost := range []int64{100, 100, 100, 200, 200, 200} {
		seedRunUsage(t, db, wid, trimmed, int64(i+1), "succeeded", int64(100+i), nil, cost, 3000/(1+int64(i)/3))
	}

	cheaperJobs, leanerJobs, err := m.gettingCheaper(t.Context(), wid, 0)
	require.NoError(t, err)
	require.Len(t, cheaperJobs, 2)
	require.Equal(t, cheaper, cheaperJobs[0].JobID)
	require.Equal(t, int64(1000), cheaperJobs[0].FirstCost)
	require.Equal(t, int64(250), cheaperJobs[0].RecentCost)
	require.InDelta(t, 0.75, cheaperJobs[0].DropPct, 0.0001)
	require.Equal(t, int64(3000), cheaperJobs[0].FirstTokens)
	require.Equal(t, int64(750), cheaperJobs[0].RecentTokens)
	require.InDelta(t, 0.75, cheaperJobs[0].TokenDropPct, 0.0001)
	require.Equal(t, 8, cheaperJobs[0].Runs)
	require.Equal(t, switched, cheaperJobs[1].JobID)
	require.InDelta(t, 2.0/3.0, cheaperJobs[1].DropPct, 0.0001)
	require.InDelta(t, -1, cheaperJobs[1].TokenDropPct, 0.0001)

	// The token comparison picks and orders its jobs by tokens alone
	require.Len(t, leanerJobs, 2)
	require.Equal(t, cheaper, leanerJobs[0].JobID)
	require.Equal(t, trimmed, leanerJobs[1].JobID)
	require.Equal(t, int64(3000), leanerJobs[1].FirstTokens)
	require.Equal(t, int64(1500), leanerJobs[1].RecentTokens)
	require.InDelta(t, 0.5, leanerJobs[1].TokenDropPct, 0.0001)
	require.InDelta(t, -1, leanerJobs[1].DropPct, 0.0001)
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

	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	out, err := m.jobStats(ctx, &jobStatsInput{ID: job, Range: "30d"})
	require.NoError(t, err)

	// Every run of the range counts, with the reflection cost of 5 and its 50 tokens per run included in the averages
	require.InDelta(t, 0.5, out.Body.SuccessRate, 0.0001)
	require.Equal(t, int64((300*1005+300*15)/600), out.Body.AvgCost)
	require.Equal(t, int64((300*3050+300*80)/600), out.Body.AvgTokens)
	require.Equal(t, int64(50), out.Body.P50Ms)

	// The chart holds the newest runs in chronological order and leaves skipped runs out
	require.Len(t, out.Body.Runs, maxJobChartRuns)
	require.Equal(t, int64(101), out.Body.Runs[0].Number)
	require.Equal(t, int64(3050), out.Body.Runs[0].Tokens)
	require.Equal(t, int64(80), out.Body.Runs[len(out.Body.Runs)-1].Tokens)
	require.Equal(t, int64(600), out.Body.Runs[len(out.Body.Runs)-1].Number)
	for i := 1; i < len(out.Body.Runs); i++ {
		require.Less(t, out.Body.Runs[i-1].QueuedAt, out.Body.Runs[i].QueuedAt)
	}
}

// The tiles above the chart show these counts, so they have to reach past the newest maxJobChartRuns runs the chart holds
func TestJobStatsCountEveryRunOfTheRangeBeyondTheChart(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db, Jobs: allowJobs{}})

	// A job on */5 * * * * runs 2016 times in 7 days, and every other run of the older half failed or timed out slowly while the newer half all succeeded quickly
	const total = 2016
	newest := time.Now().Add(-time.Minute).UnixMilli()
	step := (5 * time.Minute).Milliseconds()
	slow, fast := int64(60_000), int64(30_000)
	for i := int64(1); i <= total; i++ {
		queuedAt := newest - (total-i)*step
		switch {
		case i <= total/2 && i%4 == 0:
			seedRun(t, db, wid, job, i, "timed_out", queuedAt, &slow, 10)
		case i <= total/2 && i%2 == 0:
			seedRun(t, db, wid, job, i, "failed", queuedAt, &slow, 10)
		case i <= total/2:
			seedRun(t, db, wid, job, i, "succeeded", queuedAt, &slow, 10)
		default:
			seedRun(t, db, wid, job, i, "succeeded", queuedAt, &fast, 10)
		}
	}
	seedRun(t, db, wid, job, total+1, "skipped", newest, nil, 0)

	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	out, err := m.jobStats(ctx, &jobStatsInput{ID: job, Range: "7d"})
	require.NoError(t, err)

	// The chart stops at the newest runs, which all succeeded quickly
	require.Len(t, out.Body.Runs, maxJobChartRuns)

	// The counts and the longest duration cover the whole range without the skipped run, so they agree with the success rate
	require.Equal(t, int64(total), out.Body.RunCount)
	require.Equal(t, int64(total*3/4), out.Body.Succeeded)
	require.Equal(t, int64(total/4), out.Body.Failed)
	require.InDelta(t, 0.75, out.Body.SuccessRate, 0.0001)
	require.Equal(t, slow, out.Body.MaxMs)
}

func TestPeriodsAreWholeBucketsEndingWithTheCurrentOne(t *testing.T) {
	now := time.Date(2026, 9, 27, 13, 45, 12, 0, time.UTC).UnixMilli()
	at := func(day, hour, minute, second int) int64 {
		return time.Date(2026, 9, day, hour, minute, second, 0, time.UTC).UnixMilli()
	}

	// Seven days are today and the six days before, compared with the same stretch of time a week earlier
	week := periodOf("7d", now)
	require.Equal(t, "day", week.bucket)
	require.Equal(t, dayMs, week.bucketMs)
	require.Equal(t, at(21, 0, 0, 0), week.from)
	require.Equal(t, at(28, 0, 0, 0), week.to)
	require.Equal(t, at(14, 0, 0, 0), week.previousFrom)
	require.Equal(t, at(20, 13, 45, 12), week.previousTo)

	// The last 24 hours are this hour and the 23 before it
	day := periodOf("24h", now)
	require.Equal(t, "hour", day.bucket)
	require.Equal(t, hourMs, day.bucketMs)
	require.Equal(t, at(26, 14, 0, 0), day.from)
	require.Equal(t, at(27, 14, 0, 0), day.to)
	require.Equal(t, at(25, 14, 0, 0), day.previousFrom)
	require.Equal(t, at(26, 13, 45, 12), day.previousTo)

	for r, buckets := range map[string]int64{"24h": 24, "7d": 7, "30d": 30, "90d": 90, "": 7} {
		p := periodOf(r, now)
		require.Equal(t, buckets, (p.to-p.from)/p.bucketMs, r)
		require.Equal(t, p.to-p.from, p.from-p.previousFrom, r)
	}
}

func TestOverviewChartsAddUpToTheKPIs(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	first := testutil.SeedJob(t, db, wid, "skip")
	second := testutil.SeedJob(t, db, wid, "skip")
	m := New(Dependencies{DB: db})
	now := time.Date(2026, 9, 27, 13, 45, 12, 0, time.UTC).UnixMilli()
	at := func(day, hour, minute int) int64 {
		return time.Date(2026, 9, day, hour, minute, 0, 0, time.UTC).UnixMilli()
	}

	// Runs on both edges of the week and of the last 24 hours, and on both sides of the point the previous week is cut off at
	number := int64(0)
	seed := func(jobID, status string, queuedAt, cost int64) {
		t.Helper()
		number++
		seedRun(t, db, wid, jobID, number, status, queuedAt, nil, cost)
	}
	seed(first, "succeeded", at(20, 23, 59), 1000)
	seed(first, "failed", at(20, 12, 0), 7)
	seed(first, "succeeded", at(20, 14, 0), 9)
	seed(first, "succeeded", at(21, 0, 0), 10)
	seed(second, "failed", at(23, 8, 30), 20)
	seed(first, "timed_out", at(26, 13, 59), 30)
	seed(second, "succeeded", at(26, 14, 0), 40)
	seed(first, "running", at(27, 13, 40), 50)
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at)
		VALUES ($1, $2, $3, 99, 'skipped', 'explore', 'manual', 0, $4)`, database.NewID(), wid, second, at(27, 13, 41))

	for _, tc := range []struct {
		r        string
		buckets  int
		runs     int64
		cost     int64
		tokens   int64
		previous int64
	}{
		{r: "7d", buckets: 7, runs: 5, cost: 10 + 20 + 30 + 40 + 50 + 5*5, tokens: 3*(10+20+30+40+50) + 5*50, previous: 1},
		{r: "24h", buckets: 24, runs: 2, cost: 40 + 50 + 2*5, tokens: 3*(40+50) + 2*50, previous: 0},
	} {
		out, err := m.overviewAt(t.Context(), wid, tc.r, now)
		require.NoError(t, err)
		p := periodOf(tc.r, now)
		require.Equal(t, p.bucket, out.Bucket)
		require.Equal(t, p.from, out.From)
		require.Equal(t, p.to, out.To)
		require.Equal(t, tc.runs, out.Current.Runs, tc.r)
		require.Equal(t, tc.cost, out.Current.RunCost+out.Current.LearningCost, tc.r)
		require.Equal(t, tc.tokens, out.Current.RunTokens+out.Current.LearningTokens, tc.r)
		require.Equal(t, tc.previous, out.Previous.Runs, tc.r)

		// Every bucket is listed, oldest first, and the counts and costs add up to the KPIs
		require.Len(t, out.PerDay, tc.buckets, tc.r)
		var runs, skipped, cost, tokens int64
		for i, b := range out.PerDay {
			require.Equal(t, p.from+int64(i)*p.bucketMs, b.Day, tc.r)
			runs += b.Succeeded + b.Failed + b.Cancelled + b.TimedOut + b.Other
			skipped += b.Skipped
			cost += b.Cost
			tokens += b.Tokens
		}
		require.Equal(t, out.Current.Runs, runs, tc.r)
		require.Equal(t, int64(1), skipped, tc.r)
		require.Equal(t, tc.cost, cost, tc.r)
		require.Equal(t, tc.tokens, tokens, tc.r)

		var jobCost, jobTokens int64
		for _, c := range out.CostByJob {
			require.GreaterOrEqual(t, c.Day, p.from, tc.r)
			require.Less(t, c.Day, p.to, tc.r)
			jobCost += c.Cost
			jobTokens += c.Tokens
		}
		require.Equal(t, tc.cost, jobCost, tc.r)
		require.Equal(t, tc.tokens, jobTokens, tc.r)
	}
}

// The dashboard welcomes a fresh workspace when the overview has no runs at all, so runs older than the period and the one before it must still count
func TestOverviewTellsAWorkspaceWithOnlyOlderRunsFromAFreshOne(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	m := New(Dependencies{DB: db})
	now := time.Date(2026, 9, 27, 13, 45, 12, 0, time.UTC).UnixMilli()

	// A weekly job that succeeded three times, the last time 16 days ago, and never failed
	used := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, used, "skip")
	for i, daysAgo := range []int64{30, 23, 16} {
		ms := int64(60_000)
		seedRun(t, db, used, job, int64(i+1), "succeeded", now-daysAgo*dayMs, &ms, 100)
	}

	// The week and the one before it hold none of those runs, and nothing else in the overview hints at them
	week, err := m.overviewAt(t.Context(), used, "7d", now)
	require.NoError(t, err)
	require.Zero(t, week.Current.Runs)
	require.Zero(t, week.Previous.Runs)
	require.Empty(t, week.Running)
	require.Empty(t, week.RecentFailures)
	require.Empty(t, week.GettingCheaper)
	require.True(t, week.HasRuns)

	// A workspace that never ran anything is the only one without runs
	fresh := testutil.SeedWorkspace(t, db)
	testutil.SeedJob(t, db, fresh, "skip")
	empty, err := m.overviewAt(t.Context(), fresh, "7d", now)
	require.NoError(t, err)
	require.False(t, empty.HasRuns)
}
