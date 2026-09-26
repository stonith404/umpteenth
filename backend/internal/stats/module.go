// Package stats computes dashboard and job statistics from the runs table
package stats

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// JobChecker verifies a job belongs to the workspace
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

type Dependencies struct {
	DB   *database.DB
	Jobs JobChecker
}

type Module struct {
	deps Dependencies
	db   *database.DB
}

func New(deps Dependencies) *Module {
	return &Module{deps: deps, db: deps.DB}
}

// SetJobs wires the job checker, which is built after this module
func (m *Module) SetJobs(j JobChecker) { m.deps.Jobs = j }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("get-stats-overview", http.MethodGet, "/api/stats/overview", "Stats"), auth, m.overview)
	httpserver.Register(api, httpserver.Operation("get-job-stats", http.MethodGet, "/api/jobs/{id}/stats", "Stats"), auth, m.jobStats)
}

const dayMs = int64(24 * time.Hour / time.Millisecond)

func parseRange(r string) time.Duration {
	switch r {
	case "24h":
		return 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	case "90d":
		return 90 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

// Totals are the KPI values of one period
type Totals struct {
	Runs         int64   `json:"runs"`
	Succeeded    int64   `json:"succeeded"`
	Failed       int64   `json:"failed"`
	SuccessRate  float64 `json:"successRate"`
	P50Ms        int64   `json:"p50Ms"`
	P95Ms        int64   `json:"p95Ms"`
	RunCost      int64   `json:"runCost" doc:"Micro-USD"`
	LearningCost int64   `json:"learningCost" doc:"Micro-USD"`
}

// DayBucket counts runs of one day by status
type DayBucket struct {
	Day       int64 `json:"day"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
	TimedOut  int64 `json:"timedOut"`
	Skipped   int64 `json:"skipped"`
	Other     int64 `json:"other"`
	Cost      int64 `json:"cost"`
}

// JobCost is one job's cost on one day
type JobCost struct {
	Day     int64  `json:"day"`
	JobID   string `json:"jobId"`
	JobName string `json:"jobName"`
	Cost    int64  `json:"cost"`
}

// RunRef is a compact run reference for lists
type RunRef struct {
	ID        string  `json:"id"`
	JobID     string  `json:"jobId"`
	JobName   string  `json:"jobName"`
	Number    int64   `json:"number"`
	Status    string  `json:"status"`
	Mode      string  `json:"mode"`
	QueuedAt  int64   `json:"queuedAt"`
	StartedAt *int64  `json:"startedAt"`
	Error     *string `json:"error"`
}

// CheaperJob compares a job's early and recent average cost per run
type CheaperJob struct {
	JobID      string  `json:"jobId"`
	JobName    string  `json:"jobName"`
	FirstCost  int64   `json:"firstCost"`
	RecentCost int64   `json:"recentCost"`
	DropPct    float64 `json:"dropPct"`
	Runs       int     `json:"runs"`
}

type overviewInput struct {
	Range string `query:"range" enum:"24h,7d,30d,90d" default:"7d"`
}

type overviewOutput struct {
	Body struct {
		Range          string       `json:"range"`
		Current        Totals       `json:"current"`
		Previous       Totals       `json:"previous"`
		PerDay         []DayBucket  `json:"perDay"`
		CostByJob      []JobCost    `json:"costByJob"`
		Running        []RunRef     `json:"running"`
		RecentFailures []RunRef     `json:"recentFailures"`
		GettingCheaper []CheaperJob `json:"gettingCheaper"`
	}
}

func (m *Module) overview(ctx context.Context, in *overviewInput) (*overviewOutput, error) {
	wid := principal.WorkspaceID(ctx)
	span := parseRange(in.Range)
	now := time.Now().UnixMilli()
	from := now - span.Milliseconds()

	out := &overviewOutput{}
	out.Body.Range = in.Range
	var err error
	out.Body.Current, err = m.totals(ctx, wid, from, now)
	if err != nil {
		return nil, err
	}
	out.Body.Previous, err = m.totals(ctx, wid, from-span.Milliseconds(), from)
	if err != nil {
		return nil, err
	}
	out.Body.PerDay, err = m.perDay(ctx, wid, from)
	if err != nil {
		return nil, err
	}
	out.Body.CostByJob, err = m.costByJob(ctx, wid, from)
	if err != nil {
		return nil, err
	}
	out.Body.Running, err = m.runRefs(ctx, "r.workspace_id = $1 AND r.status IN ('queued', 'provisioning', 'running', 'verifying') ORDER BY r.queued_at", wid)
	if err != nil {
		return nil, err
	}
	out.Body.RecentFailures, err = m.runRefs(ctx, "r.workspace_id = $1 AND r.status IN ('failed', 'timed_out') ORDER BY r.queued_at DESC LIMIT 5", wid)
	if err != nil {
		return nil, err
	}
	out.Body.GettingCheaper, err = m.gettingCheaper(ctx, wid, now-90*dayMs)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Module) totals(ctx context.Context, wid string, from, to int64) (Totals, error) {
	var t Totals
	const where = `workspace_id = $1 AND queued_at >= $2 AND queued_at < $3 AND status <> 'skipped'`

	// Counts and sums are aggregated in the database, so large ranges never stream every run to the replica
	var timed int64
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		   COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status IN ('failed', 'timed_out') THEN 1 ELSE 0 END), 0),
		   COUNT(ms_total), COALESCE(SUM(cost), 0), COALESCE(SUM(reflection_cost + verify_cost), 0)
		 FROM runs WHERE `+where, wid, from, to).
		Scan(&t.Runs, &t.Succeeded, &t.Failed, &timed, &t.RunCost, &t.LearningCost)
	if err != nil {
		return t, fmt.Errorf("failed to load totals: %w", err)
	}
	if finished := t.Succeeded + t.Failed; finished > 0 {
		t.SuccessRate = float64(t.Succeeded) / float64(finished)
	}

	// Percentiles are picked by offset, which works the same on both engines
	for _, p := range []struct {
		q   float64
		dst *int64
	}{{0.5, &t.P50Ms}, {0.95, &t.P95Ms}} {
		if timed == 0 {
			break
		}
		offset := int64(float64(timed-1) * p.q)
		err = m.db.QueryRowContext(ctx, `SELECT ms_total FROM runs WHERE `+where+` AND ms_total IS NOT NULL ORDER BY ms_total LIMIT 1 OFFSET $4`, wid, from, to, offset).Scan(p.dst)
		if err != nil {
			return t, fmt.Errorf("failed to load duration percentile: %w", err)
		}
	}
	return t, nil
}

// dayStart rounds a unix millisecond timestamp down to the start of its UTC day
func dayStart(ts int64) int64 {
	return (ts / dayMs) * dayMs
}

func (m *Module) perDay(ctx context.Context, wid string, from int64) ([]DayBucket, error) {
	// The series starts at the beginning of from's day, so the first bucket holds the whole day like every other bucket
	from = dayStart(from)
	rows, err := m.db.QueryContext(ctx,
		`SELECT (queued_at / $2) * $2 AS day, status, COUNT(*), COALESCE(SUM(cost + reflection_cost + verify_cost), 0)
		 FROM runs WHERE workspace_id = $1 AND queued_at >= $3 GROUP BY day, status ORDER BY day`, wid, dayMs, from)
	if err != nil {
		return nil, fmt.Errorf("failed to load runs per day: %w", err)
	}
	defer rows.Close()

	buckets := map[int64]*DayBucket{}
	for rows.Next() {
		var (
			day, count, cost int64
			status           string
		)
		if err := rows.Scan(&day, &status, &count, &cost); err != nil {
			return nil, err
		}
		b, ok := buckets[day]
		if !ok {
			b = &DayBucket{Day: day}
			buckets[day] = b
		}
		b.Cost += cost
		switch status {
		case "succeeded":
			b.Succeeded += count
		case "failed":
			b.Failed += count
		case "cancelled":
			b.Cancelled += count
		case "timed_out":
			b.TimedOut += count
		case "skipped":
			b.Skipped += count
		default:
			b.Other += count
		}
	}

	// Every day of the period gets a bucket, so charts have no gaps
	out := []DayBucket{}
	for day := from; day <= time.Now().UnixMilli(); day += dayMs {
		if b, ok := buckets[day]; ok {
			out = append(out, *b)
		} else {
			out = append(out, DayBucket{Day: day})
		}
	}
	return out, rows.Err()
}

func (m *Module) costByJob(ctx context.Context, wid string, from int64) ([]JobCost, error) {
	// Whole days are summed, matching the buckets of perDay
	from = dayStart(from)
	rows, err := m.db.QueryContext(ctx,
		`SELECT (r.queued_at / $2) * $2 AS day, r.job_id, j.name, COALESCE(SUM(r.cost + r.reflection_cost + r.verify_cost), 0)
		 FROM runs r JOIN jobs j ON j.id = r.job_id
		 WHERE r.workspace_id = $1 AND r.queued_at >= $3 GROUP BY day, r.job_id, j.name ORDER BY day`, wid, dayMs, from)
	if err != nil {
		return nil, fmt.Errorf("failed to load cost by job: %w", err)
	}
	defer rows.Close()
	out := []JobCost{}
	for rows.Next() {
		var c JobCost
		if err := rows.Scan(&c.Day, &c.JobID, &c.JobName, &c.Cost); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m *Module) runRefs(ctx context.Context, where string, args ...any) ([]RunRef, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT r.id, r.job_id, j.name, r.number, r.status, r.mode, r.queued_at, r.started_at, r.error
		 FROM runs r JOIN jobs j ON j.id = r.job_id WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to load runs: %w", err)
	}
	defer rows.Close()
	out := []RunRef{}
	for rows.Next() {
		var r RunRef
		if err := rows.Scan(&r.ID, &r.JobID, &r.JobName, &r.Number, &r.Status, &r.Mode, &r.QueuedAt, &r.StartedAt, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// gettingCheaper compares the average cost of each job's first and latest three successful runs
func (m *Module) gettingCheaper(ctx context.Context, wid string, from int64) ([]CheaperJob, error) {
	// Each job's count and both averages come from short range reads on runs_job_status_queued, so the cost grows with jobs rather than runs
	rows, err := m.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.run_count, t.first_sum, t.recent_sum FROM (
		   SELECT j.id, j.name,
		     (SELECT COUNT(*) FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2) AS run_count,
		     (SELECT CAST(COALESCE(SUM(f.cost), 0) AS BIGINT) FROM (SELECT r.cost FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at LIMIT 3) f) AS first_sum,
		     (SELECT CAST(COALESCE(SUM(l.cost), 0) AS BIGINT) FROM (SELECT r.cost FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at DESC LIMIT 3) l) AS recent_sum
		   FROM jobs j WHERE j.workspace_id = $1
		 ) t
		 WHERE t.run_count >= $3 AND t.first_sum > 0 AND t.recent_sum < t.first_sum`, wid, from, minCheaperRuns)
	if err != nil {
		return nil, fmt.Errorf("failed to load run costs: %w", err)
	}
	defer rows.Close()

	out := []CheaperJob{}
	for rows.Next() {
		var c CheaperJob
		if err := rows.Scan(&c.JobID, &c.JobName, &c.Runs, &c.FirstCost, &c.RecentCost); err != nil {
			return nil, err
		}
		// The sums cover exactly three runs each, and dividing here keeps integer rounding identical on both engines
		c.FirstCost, c.RecentCost = c.FirstCost/3, c.RecentCost/3
		c.DropPct = 1 - float64(c.RecentCost)/float64(c.FirstCost)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DropPct > out[j].DropPct })
	if len(out) > 5 {
		out = out[:5]
	}
	return out, nil
}

// minCheaperRuns keeps the first and latest three runs apart, so a job is never compared with itself
const minCheaperRuns = 6

// JobRunPoint is one run on the job's graduation chart
type JobRunPoint struct {
	RunID           string `json:"runId"`
	Number          int64  `json:"number"`
	QueuedAt        int64  `json:"queuedAt"`
	Status          string `json:"status"`
	Mode            string `json:"mode"`
	Cost            int64  `json:"cost"`
	MsTotal         *int64 `json:"msTotal"`
	Turns           int64  `json:"turns"`
	PlaybookVersion int64  `json:"playbookVersion"`
}

// VersionMarker marks when a playbook version was created
type VersionMarker struct {
	Version   int64  `json:"version"`
	Author    string `json:"author"`
	CreatedAt int64  `json:"createdAt"`
}

type jobStatsInput struct {
	ID    string `path:"id"`
	Range string `query:"range" enum:"7d,30d,90d" default:"30d"`
}

type jobStatsOutput struct {
	Body struct {
		Runs        []JobRunPoint   `json:"runs"`
		Versions    []VersionMarker `json:"versions"`
		SuccessRate float64         `json:"successRate"`
		AvgCost     int64           `json:"avgCost"`
		P50Ms       int64           `json:"p50Ms"`
	}
}

// maxJobChartRuns bounds the graduation chart, which shows the newest runs of the range
const maxJobChartRuns = 500

func (m *Module) jobStats(ctx context.Context, in *jobStatsInput) (*jobStatsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	from := time.Now().Add(-parseRange(in.Range)).UnixMilli()

	// The KPIs cover every run of the range, independently of how many runs the chart shows
	out := &jobStatsOutput{}
	var err error
	out.Body.SuccessRate, out.Body.AvgCost, out.Body.P50Ms, err = m.jobSummary(ctx, wid, in.ID, from)
	if err != nil {
		return nil, err
	}

	out.Body.Runs, err = m.jobRunPoints(ctx, wid, in.ID, from)
	if err != nil {
		return nil, err
	}

	vrows, err := m.db.QueryContext(ctx, `SELECT version, author, created_at FROM playbook_versions WHERE job_id = $1 AND created_at >= $2 ORDER BY version`, in.ID, from)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	out.Body.Versions = []VersionMarker{}
	for vrows.Next() {
		var v VersionMarker
		if err := vrows.Scan(&v.Version, &v.Author, &v.CreatedAt); err != nil {
			return nil, err
		}
		out.Body.Versions = append(out.Body.Versions, v)
	}
	return out, vrows.Err()
}

// jobSummary aggregates a job's success rate, average cost and median duration over every non-skipped run since from
func (m *Module) jobSummary(ctx context.Context, wid, jobID string, from int64) (successRate float64, avgCost int64, p50 int64, err error) {
	const where = `workspace_id = $1 AND job_id = $2 AND queued_at >= $3 AND status <> 'skipped'`

	// Counts and sums are aggregated in the database, so a busy job never streams every run of the range
	var runs, succeeded, finished, costSum, timed int64
	err = m.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		   COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status IN ('succeeded', 'failed', 'timed_out') THEN 1 ELSE 0 END), 0),
		   CAST(COALESCE(SUM(cost + reflection_cost + verify_cost), 0) AS BIGINT), COUNT(ms_total)
		 FROM runs WHERE `+where, wid, jobID, from).
		Scan(&runs, &succeeded, &finished, &costSum, &timed)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to load job totals: %w", err)
	}
	if finished > 0 {
		successRate = float64(succeeded) / float64(finished)
	}
	if runs > 0 {
		avgCost = costSum / runs
	}

	// The median is picked by offset, which works the same on both engines
	if timed > 0 {
		offset := (timed - 1) / 2
		err = m.db.QueryRowContext(ctx, `SELECT ms_total FROM runs WHERE `+where+` AND ms_total IS NOT NULL ORDER BY ms_total LIMIT 1 OFFSET $4`, wid, jobID, from, offset).Scan(&p50)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("failed to load job median duration: %w", err)
		}
	}
	return successRate, avgCost, p50, nil
}

// jobRunPoints returns the newest runs of the range in chronological order, so the chart always reaches the present
func (m *Module) jobRunPoints(ctx context.Context, wid, jobID string, from int64) ([]JobRunPoint, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, number, queued_at, status, mode, cost + reflection_cost + verify_cost, ms_total, turns, playbook_version FROM runs
		 WHERE workspace_id = $1 AND job_id = $2 AND queued_at >= $3 AND status <> 'skipped' ORDER BY queued_at DESC LIMIT $4`, wid, jobID, from, maxJobChartRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := []JobRunPoint{}
	for rows.Next() {
		var p JobRunPoint
		if err := rows.Scan(&p.RunID, &p.Number, &p.QueuedAt, &p.Status, &p.Mode, &p.Cost, &p.MsTotal, &p.Turns, &p.PlaybookVersion); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The query reads newest first to pick the latest runs, and the chart wants them oldest first
	slices.Reverse(points)
	return points, nil
}
