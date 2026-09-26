// Package stats computes dashboard and job statistics from the runs table
package stats

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
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

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("get-stats-overview", http.MethodGet, "/api/stats/overview", "Stats"), auth, m.overview)
	httpserver.Register(api, httpserver.Operation("get-job-stats", http.MethodGet, "/api/jobs/{id}/stats", "Stats"), auth, m.jobStats)
}

// Widths of the overview's chart buckets
const (
	hourMs = int64(time.Hour / time.Millisecond)
	dayMs  = int64(24 * time.Hour / time.Millisecond)
)

// parseRange returns the length of a job stats range, which counts back from the present
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
// Every cost has a token count next to it, input plus output tokens, so the UI shows whichever unit the workspace chose
type Totals struct {
	Runs           int64   `json:"runs"`
	Succeeded      int64   `json:"succeeded"`
	Failed         int64   `json:"failed"`
	SuccessRate    float64 `json:"successRate"`
	P50Ms          int64   `json:"p50Ms"`
	P95Ms          int64   `json:"p95Ms"`
	RunCost        int64   `json:"runCost" doc:"Micro-USD"`
	LearningCost   int64   `json:"learningCost" doc:"Micro-USD"`
	RunTokens      int64   `json:"runTokens" doc:"Input and output tokens of the runs, like runCost"`
	LearningTokens int64   `json:"learningTokens" doc:"Input and output tokens of reflection and verification, like learningCost"`
}

// DayBucket counts the runs of one chart bucket by status, a UTC day or, for the 24h range, an hour
type DayBucket struct {
	Day       int64 `json:"day" doc:"Start of the bucket in unix milliseconds, a UTC day or, for the 24h range, an hour"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
	TimedOut  int64 `json:"timedOut"`
	Skipped   int64 `json:"skipped"`
	Other     int64 `json:"other"`
	Cost      int64 `json:"cost" doc:"Micro-USD, including learning"`
	Tokens    int64 `json:"tokens" doc:"Input and output tokens, including learning, like cost"`
}

// JobCost is one job's cost and tokens in one chart bucket
type JobCost struct {
	Day     int64  `json:"day" doc:"Start of the bucket in unix milliseconds, like DayBucket.day"`
	JobID   string `json:"jobId"`
	JobName string `json:"jobName"`
	Cost    int64  `json:"cost" doc:"Micro-USD, including learning"`
	Tokens  int64  `json:"tokens" doc:"Input and output tokens, including learning, like cost"`
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
	// Summary is the agent's own account of the run, which explains a failure better than the generic reason in Error
	Summary *string `json:"summary"`
}

// CheaperJob compares a job's early and recent average cost and tokens per run
type CheaperJob struct {
	JobID        string  `json:"jobId"`
	JobName      string  `json:"jobName"`
	FirstCost    int64   `json:"firstCost" doc:"Average cost of the first three successful runs, in micro-USD"`
	RecentCost   int64   `json:"recentCost" doc:"Average cost of the latest three successful runs, in micro-USD"`
	DropPct      float64 `json:"dropPct" doc:"How much less the latest runs cost, from 0 to 1"`
	FirstTokens  int64   `json:"firstTokens" doc:"Average input and output tokens of the first three successful runs"`
	RecentTokens int64   `json:"recentTokens" doc:"Average input and output tokens of the latest three successful runs"`
	TokenDropPct float64 `json:"tokenDropPct" doc:"How many fewer tokens the latest runs used, from 0 to 1, and negative when they used more"`
	Runs         int     `json:"runs"`
}

type overviewInput struct {
	Range string `query:"range" enum:"24h,7d,30d,90d" default:"7d"`
}

// overviewOutputBody is the dashboard overview of one range
type overviewOutputBody struct {
	Range string `json:"range"`
	// Bucket and BucketMs tell the dashboard how to title and label its charts
	Bucket   string `json:"bucket" enum:"hour,day" doc:"Width of the perDay and costByJob buckets: an hour for the 24h range and a UTC day for the others"`
	BucketMs int64  `json:"bucketMs" doc:"Width of one bucket in milliseconds"`
	From     int64  `json:"from" doc:"Start of the period that current, perDay and costByJob cover, the start of its first bucket, in unix milliseconds"`
	To       int64  `json:"to" doc:"End of the period, exclusive: the end of the bucket that holds the present, in unix milliseconds"`
	Current  Totals `json:"current"`
	// Previous covers the same stretch of time one period earlier, so a period that has only just begun is compared like for like
	Previous       Totals       `json:"previous" doc:"The period before, cut off at the point in time the current period has reached"`
	PerDay         []DayBucket  `json:"perDay" doc:"One bucket for every hour or day of the period, oldest first, whose runs other than skipped ones add up to current.runs"`
	CostByJob      []JobCost    `json:"costByJob"`
	Running        []RunRef     `json:"running"`
	RecentFailures []RunRef     `json:"recentFailures"`
	GettingCheaper []CheaperJob `json:"gettingCheaper" doc:"Up to five jobs whose latest successful runs cost less than their first ones, the biggest drop in cost first"`
	// A workspace that shows usage in tokens compares tokens, which can drop for other jobs than the cost
	GettingLeaner []CheaperJob `json:"gettingLeaner" doc:"Up to five jobs whose latest successful runs used fewer tokens than their first ones, the biggest drop in tokens first"`
	// Every other field covers a stretch of time, so only this one tells a workspace whose runs are all older apart from one that never ran anything
	HasRuns bool `json:"hasRuns" doc:"Whether the workspace has any run at all, from any date"`
}

type overviewOutput struct {
	Body overviewOutputBody
}

// period is the stretch of time an overview covers, whole chart buckets that end with the one holding the present
type period struct {
	// from and to bound the runs of the period, and to is the end of the current bucket
	from, to int64
	bucket   string
	bucketMs int64
	// previousFrom and previousTo bound the period the KPI deltas compare with, which is as long as the part of the current period that has passed
	previousFrom, previousTo int64
}

// periodOf returns the period of a range at now: the last 24 hours in whole hours, and the other ranges in whole UTC days including today
// The KPIs and the charts share it, so the chart buckets always add up to the KPIs
func periodOf(r string, now int64) period {
	bucket, width, count := "day", dayMs, int64(7)
	switch r {
	case "24h":
		bucket, width, count = "hour", hourMs, 24
	case "30d":
		count = 30
	case "90d":
		count = 90
	}
	current := now / width * width
	from := current - (count-1)*width
	span := count * width
	return period{from: from, to: current + width, bucket: bucket, bucketMs: width, previousFrom: from - span, previousTo: now - span}
}

func (m *Module) overview(ctx context.Context, in *overviewInput) (*overviewOutput, error) {
	body, err := m.overviewAt(ctx, principal.WorkspaceID(ctx), in.Range, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return &overviewOutput{Body: body}, nil
}

// overviewAt builds the overview of a range as it looks at now
func (m *Module) overviewAt(ctx context.Context, wid, r string, now int64) (overviewOutputBody, error) {
	p := periodOf(r, now)
	out := overviewOutputBody{Range: r, Bucket: p.bucket, BucketMs: p.bucketMs, From: p.from, To: p.to}
	var err error
	out.Current, err = m.totals(ctx, wid, p.from, p.to)
	if err != nil {
		return out, err
	}
	out.Previous, err = m.totals(ctx, wid, p.previousFrom, p.previousTo)
	if err != nil {
		return out, err
	}
	out.PerDay, err = m.perBucket(ctx, wid, p)
	if err != nil {
		return out, err
	}
	out.CostByJob, err = m.costByJob(ctx, wid, p)
	if err != nil {
		return out, err
	}
	out.Running, err = m.runRefs(ctx, "r.workspace_id = $1 AND r.status IN ('queued', 'provisioning', 'running', 'verifying') ORDER BY r.queued_at", wid)
	if err != nil {
		return out, err
	}
	out.RecentFailures, err = m.runRefs(ctx, "r.workspace_id = $1 AND r.status IN ('failed', 'timed_out') ORDER BY r.queued_at DESC LIMIT 5", wid)
	if err != nil {
		return out, err
	}
	out.GettingCheaper, out.GettingLeaner, err = m.gettingCheaper(ctx, wid, now-90*dayMs)
	if err != nil {
		return out, err
	}

	// This is read last, so it sees at least every run the figures above counted
	err = m.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE workspace_id = $1)`, wid).Scan(&out.HasRuns)
	if err != nil {
		return out, fmt.Errorf("failed to check for runs: %w", err)
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
		   COUNT(ms_total), COALESCE(SUM(cost), 0), COALESCE(SUM(reflection_cost + verify_cost), 0),
		   COALESCE(SUM(tok_in + tok_out), 0), COALESCE(SUM(reflection_tokens + verify_tokens), 0)
		 FROM runs WHERE `+where, wid, from, to).
		Scan(&t.Runs, &t.Succeeded, &t.Failed, &timed, &t.RunCost, &t.LearningCost, &t.RunTokens, &t.LearningTokens)
	if err != nil {
		return t, fmt.Errorf("failed to load totals: %w", err)
	}
	if finished := t.Succeeded + t.Failed; finished > 0 {
		t.SuccessRate = float64(t.Succeeded) / float64(finished)
	}

	// Percentiles are picked by offset, which works the same on both engines
	if timed == 0 {
		return t, nil
	}
	for _, p := range []struct {
		q   float64
		dst *int64
	}{{0.5, &t.P50Ms}, {0.95, &t.P95Ms}} {
		offset := int64(float64(timed-1) * p.q)
		err = m.db.QueryRowContext(ctx, `SELECT ms_total FROM runs WHERE `+where+` AND ms_total IS NOT NULL ORDER BY ms_total LIMIT 1 OFFSET $4`, wid, from, to, offset).Scan(p.dst)
		if err != nil {
			return t, fmt.Errorf("failed to load duration percentile: %w", err)
		}
	}
	return t, nil
}

// perBucket counts the period's runs by status in each of its buckets
func (m *Module) perBucket(ctx context.Context, wid string, p period) ([]DayBucket, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT (queued_at / $2) * $2 AS day, status, COUNT(*), COALESCE(SUM(cost + reflection_cost + verify_cost), 0),
		   COALESCE(SUM(tok_in + tok_out + reflection_tokens + verify_tokens), 0)
		 FROM runs WHERE workspace_id = $1 AND queued_at >= $3 AND queued_at < $4 GROUP BY day, status ORDER BY day`, wid, p.bucketMs, p.from, p.to)
	if err != nil {
		return nil, fmt.Errorf("failed to load runs per bucket: %w", err)
	}
	defer rows.Close()

	buckets := map[int64]*DayBucket{}
	for rows.Next() {
		var (
			day, count, cost, tokens int64
			status                   string
		)
		if err := rows.Scan(&day, &status, &count, &cost, &tokens); err != nil {
			return nil, err
		}
		b, ok := buckets[day]
		if !ok {
			b = &DayBucket{Day: day}
			buckets[day] = b
		}
		b.Cost += cost
		b.Tokens += tokens
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

	// Every bucket of the period is listed, so charts have no gaps
	out := []DayBucket{}
	for start := p.from; start < p.to; start += p.bucketMs {
		if b, ok := buckets[start]; ok {
			out = append(out, *b)
		} else {
			out = append(out, DayBucket{Day: start})
		}
	}
	return out, rows.Err()
}

// costByJob sums each job's cost and tokens in each bucket of the period, matching the buckets of perBucket
func (m *Module) costByJob(ctx context.Context, wid string, p period) ([]JobCost, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT (r.queued_at / $2) * $2 AS day, r.job_id, j.name, COALESCE(SUM(r.cost + r.reflection_cost + r.verify_cost), 0),
		   COALESCE(SUM(r.tok_in + r.tok_out + r.reflection_tokens + r.verify_tokens), 0)
		 FROM runs r JOIN jobs j ON j.id = r.job_id
		 WHERE r.workspace_id = $1 AND r.queued_at >= $3 AND r.queued_at < $4 GROUP BY day, r.job_id, j.name ORDER BY day`, wid, p.bucketMs, p.from, p.to)
	if err != nil {
		return nil, fmt.Errorf("failed to load cost by job: %w", err)
	}
	defer rows.Close()
	out := []JobCost{}
	for rows.Next() {
		var c JobCost
		if err := rows.Scan(&c.Day, &c.JobID, &c.JobName, &c.Cost, &c.Tokens); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// runRefs lists runs matching where, a constant SQL condition whose values are always passed as args
func (m *Module) runRefs(ctx context.Context, where string, args ...any) ([]RunRef, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT r.id, r.job_id, j.name, r.number, r.status, r.mode, r.queued_at, r.started_at, r.error, r.summary
		 FROM runs r JOIN jobs j ON j.id = r.job_id WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to load runs: %w", err)
	}
	defer rows.Close()
	out := []RunRef{}
	for rows.Next() {
		var r RunRef
		if err := rows.Scan(&r.ID, &r.JobID, &r.JobName, &r.Number, &r.Status, &r.Mode, &r.QueuedAt, &r.StartedAt, &r.Error, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// gettingCheaper compares the average cost and tokens of each job's first and latest three successful runs
// It returns the jobs whose runs got cheaper and the jobs whose runs got leaner in tokens, each list with the biggest drop first
func (m *Module) gettingCheaper(ctx context.Context, wid string, from int64) (cheaper, leaner []CheaperJob, err error) {
	// Each job's count and sums come from short range reads on runs_job_status_queued, so the cost grows with jobs rather than runs
	rows, err := m.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.run_count, t.first_cost, t.recent_cost, t.first_tokens, t.recent_tokens FROM (
		   SELECT j.id, j.name,
		     (SELECT COUNT(*) FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2) AS run_count,
		     (SELECT CAST(COALESCE(SUM(f.cost), 0) AS BIGINT) FROM (SELECT r.cost FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at LIMIT 3) f) AS first_cost,
		     (SELECT CAST(COALESCE(SUM(l.cost), 0) AS BIGINT) FROM (SELECT r.cost FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at DESC LIMIT 3) l) AS recent_cost,
		     (SELECT CAST(COALESCE(SUM(f.tokens), 0) AS BIGINT) FROM (SELECT r.tok_in + r.tok_out AS tokens FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at LIMIT 3) f) AS first_tokens,
		     (SELECT CAST(COALESCE(SUM(l.tokens), 0) AS BIGINT) FROM (SELECT r.tok_in + r.tok_out AS tokens FROM runs r WHERE r.job_id = j.id AND r.status = 'succeeded' AND r.queued_at >= $2 ORDER BY r.queued_at DESC LIMIT 3) l) AS recent_tokens
		   FROM jobs j WHERE j.workspace_id = $1
		 ) t
		 WHERE t.run_count >= $3 AND ((t.first_cost > 0 AND t.recent_cost < t.first_cost) OR (t.first_tokens > 0 AND t.recent_tokens < t.first_tokens))`, wid, from, minCheaperRuns)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load run costs: %w", err)
	}
	defer rows.Close()

	cheaper, leaner = []CheaperJob{}, []CheaperJob{}
	for rows.Next() {
		var (
			c                                                CheaperJob
			firstCost, recentCost, firstTokens, recentTokens int64
		)
		if err := rows.Scan(&c.JobID, &c.JobName, &c.Runs, &firstCost, &recentCost, &firstTokens, &recentTokens); err != nil {
			return nil, nil, err
		}

		// The sums cover exactly three runs each, and dividing here keeps integer rounding identical on both engines
		c.FirstCost, c.RecentCost, c.FirstTokens, c.RecentTokens = firstCost/3, recentCost/3, firstTokens/3, recentTokens/3

		// The drops come from the sums, so averages that round down to 0 never divide by zero
		if firstCost > 0 {
			c.DropPct = 1 - float64(recentCost)/float64(firstCost)
		}
		if firstTokens > 0 {
			c.TokenDropPct = 1 - float64(recentTokens)/float64(firstTokens)
		}

		// A job can get cheaper without getting leaner, such as after a switch to a cheaper model, so it joins each list on its own merit
		if firstCost > 0 && recentCost < firstCost {
			cheaper = append(cheaper, c)
		}
		if firstTokens > 0 && recentTokens < firstTokens {
			leaner = append(leaner, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return topDrops(cheaper, func(c CheaperJob) float64 { return c.DropPct }), topDrops(leaner, func(c CheaperJob) float64 { return c.TokenDropPct }), nil
}

// topDrops keeps the five jobs with the biggest drop
func topDrops(jobs []CheaperJob, drop func(CheaperJob) float64) []CheaperJob {
	slices.SortFunc(jobs, func(a, b CheaperJob) int { return cmp.Compare(drop(b), drop(a)) })
	return jobs[:min(len(jobs), 5)]
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
	Cost            int64  `json:"cost" doc:"Micro-USD, including learning"`
	Tokens          int64  `json:"tokens" doc:"Input and output tokens, including learning, like cost"`
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

// jobStatsOutput carries the counts of every run of the range next to the chart's runs, which stop at the newest maxJobChartRuns
type jobStatsOutput struct {
	Body struct {
		Runs        []JobRunPoint   `json:"runs"`
		Versions    []VersionMarker `json:"versions"`
		RunCount    int64           `json:"runCount" doc:"Runs of the range other than skipped ones"`
		Succeeded   int64           `json:"succeeded"`
		Failed      int64           `json:"failed" doc:"Failed and timed out runs"`
		SuccessRate float64         `json:"successRate"`
		AvgCost     int64           `json:"avgCost" doc:"Average cost per run in micro-USD, including learning"`
		AvgTokens   int64           `json:"avgTokens" doc:"Average input and output tokens per run, including learning, like avgCost"`
		P50Ms       int64           `json:"p50Ms"`
		MaxMs       int64           `json:"maxMs" doc:"Duration of the longest run"`
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
	err := m.jobSummary(ctx, wid, in.ID, from, out)
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

// jobSummary aggregates a job's run counts, success rate, average cost and tokens and median and longest duration over every non-skipped run since from
func (m *Module) jobSummary(ctx context.Context, wid, jobID string, from int64, out *jobStatsOutput) error {
	const where = `workspace_id = $1 AND job_id = $2 AND queued_at >= $3 AND status <> 'skipped'`

	// Counts and sums are aggregated in the database, so a busy job never streams every run of the range
	var costSum, tokenSum, timed int64
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		   COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status IN ('failed', 'timed_out') THEN 1 ELSE 0 END), 0),
		   CAST(COALESCE(SUM(cost + reflection_cost + verify_cost), 0) AS BIGINT),
		   CAST(COALESCE(SUM(tok_in + tok_out + reflection_tokens + verify_tokens), 0) AS BIGINT), COUNT(ms_total), COALESCE(MAX(ms_total), 0)
		 FROM runs WHERE `+where, wid, jobID, from).
		Scan(&out.Body.RunCount, &out.Body.Succeeded, &out.Body.Failed, &costSum, &tokenSum, &timed, &out.Body.MaxMs)
	if err != nil {
		return fmt.Errorf("failed to load job totals: %w", err)
	}
	if finished := out.Body.Succeeded + out.Body.Failed; finished > 0 {
		out.Body.SuccessRate = float64(out.Body.Succeeded) / float64(finished)
	}
	if out.Body.RunCount > 0 {
		out.Body.AvgCost, out.Body.AvgTokens = costSum/out.Body.RunCount, tokenSum/out.Body.RunCount
	}

	// The median is picked by offset, which works the same on both engines
	if timed > 0 {
		offset := (timed - 1) / 2
		err = m.db.QueryRowContext(ctx, `SELECT ms_total FROM runs WHERE `+where+` AND ms_total IS NOT NULL ORDER BY ms_total LIMIT 1 OFFSET $4`, wid, jobID, from, offset).Scan(&out.Body.P50Ms)
		if err != nil {
			return fmt.Errorf("failed to load job median duration: %w", err)
		}
	}
	return nil
}

// jobRunPoints returns the newest runs of the range in chronological order, so the chart always reaches the present
func (m *Module) jobRunPoints(ctx context.Context, wid, jobID string, from int64) ([]JobRunPoint, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, number, queued_at, status, mode, cost + reflection_cost + verify_cost, tok_in + tok_out + reflection_tokens + verify_tokens, ms_total, turns, playbook_version FROM runs
		 WHERE workspace_id = $1 AND job_id = $2 AND queued_at >= $3 AND status <> 'skipped' ORDER BY queued_at DESC LIMIT $4`, wid, jobID, from, maxJobChartRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := []JobRunPoint{}
	for rows.Next() {
		var p JobRunPoint
		if err := rows.Scan(&p.RunID, &p.Number, &p.QueuedAt, &p.Status, &p.Mode, &p.Cost, &p.Tokens, &p.MsTotal, &p.Turns, &p.PlaybookVersion); err != nil {
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
