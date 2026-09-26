package jobs

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

// recentRunsPerJob is how many runs each row of the jobs list shows in its history strip
const recentRunsPerJob = 10

// listModeInput is what the jobs list reads with each job to decide its next mode
type listModeInput struct {
	// content is the job's current playbook, nil when it has none yet
	content          *string
	graduatedVersion *int64
}

// perJobUnion joins one branch per job with UNION ALL, and each branch stops after its own LIMIT
// A window function numbering the runs of the page's jobs would read every run they ever had, while each branch here reads a few entries of the runs_job_queued index
// branch is a SELECT in which $1 is the workspace, $2 the limit, %[1]s the job's placeholder and %[2]s the job's extra placeholder
func perJobUnion(branch, wid string, limit int, jobIDs []string, extra func(jobID string) any) (string, []any) {
	args := []any{wid, limit}
	branches := make([]string, len(jobIDs))
	for i, id := range jobIDs {
		args = append(args, id)
		jobArg := fmt.Sprintf("$%d", len(args))
		extraArg := ""
		if extra != nil {
			args = append(args, extra(id))
			extraArg = fmt.Sprintf("$%d", len(args))
		}
		branches[i] = fmt.Sprintf("SELECT * FROM ("+branch+") b%[3]d", jobArg, extraArg, i)
	}
	return strings.Join(branches, " UNION ALL "), args
}

// addRecentRuns fills each job's latest runs, and the last run and mode derived from them
func (m *Module) addRecentRuns(ctx context.Context, wid string, items []JobListDto) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for i, d := range items {
		ids[i] = d.ID
	}

	// Every job's latest runs come from one query
	query, args := perJobUnion(
		"SELECT id, job_id, number, status, mode, queued_at FROM runs WHERE workspace_id = $1 AND job_id = %[1]s ORDER BY queued_at DESC, id DESC LIMIT $2",
		wid, recentRunsPerJob, ids, nil)
	rows, err := m.deps.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to load recent runs: %w", err)
	}
	defer rows.Close()
	type listRun struct {
		LastRun
		mode string
	}
	byJob := make(map[string][]listRun, len(items))
	for rows.Next() {
		var (
			r     listRun
			jobID string
		)
		err = rows.Scan(&r.ID, &jobID, &r.Number, &r.Status, &r.mode, &r.QueuedAt)
		if err != nil {
			return fmt.Errorf("failed to scan recent run: %w", err)
		}
		byJob[jobID] = append(byJob[jobID], r)
	}
	err = rows.Err()
	if err != nil {
		return fmt.Errorf("failed to load recent runs: %w", err)
	}

	// UNION ALL does not promise to keep each branch's order, so each job's runs are put newest first again
	for i := range items {
		runs := byJob[items[i].ID]
		slices.SortFunc(runs, func(a, b listRun) int {
			return cmp.Or(cmp.Compare(b.QueuedAt, a.QueuedAt), cmp.Compare(b.ID, a.ID))
		})
		items[i].RecentRuns = make([]LastRun, len(runs))
		for j, r := range runs {
			items[i].RecentRuns[j] = r.LastRun
		}
		if len(runs) > 0 {
			items[i].LastRun = &items[i].RecentRuns[0]
			items[i].LastMode = &runs[0].mode
		}
	}
	return nil
}

// addNextModes decides the mode of each job's next run with the rules of the job page
func (m *Module) addNextModes(ctx context.Context, wid string, items []JobListDto, inputs []listModeInput) error {
	// Each job's current playbook is parsed like the playbook module loads it, and a job without one has an empty playbook
	contents := make([]playbook.Content, len(items))
	graduated := map[string]int64{}
	var demotable []string
	for i, d := range items {
		if inputs[i].content != nil {
			err := json.Unmarshal([]byte(*inputs[i].content), &contents[i])
			if err != nil {
				return fmt.Errorf("invalid playbook content of job %s: %w", d.ID, err)
			}
		}
		contents[i].Normalize()

		// Demotion only changes the mode of a job with a main script that it may run
		if contents[i].Main != nil && d.Graduate {
			graduated[d.ID] = deref(inputs[i].graduatedVersion)
			demotable = append(demotable, d.ID)
		}
	}

	// The demotion check reads the same runs as RecentScriptedRuns, for every graduated job at once
	demoted := map[string]bool{}
	if len(demotable) > 0 {
		query, args := perJobUnion(
			"SELECT job_id, fell_back FROM runs WHERE workspace_id = $1 AND job_id = %[1]s AND mode = 'scripted' AND playbook_version >= %[2]s AND status IN ('succeeded', 'failed', 'timed_out') ORDER BY number DESC LIMIT $2",
			wid, 2, demotable, func(jobID string) any { return graduated[jobID] })
		rows, err := m.deps.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to load recent scripted runs: %w", err)
		}
		defer rows.Close()
		fellBack := map[string][]bool{}
		for rows.Next() {
			var (
				jobID string
				fell  bool
			)
			err = rows.Scan(&jobID, &fell)
			if err != nil {
				return fmt.Errorf("failed to scan recent scripted run: %w", err)
			}
			fellBack[jobID] = append(fellBack[jobID], fell)
		}
		err = rows.Err()
		if err != nil {
			return fmt.Errorf("failed to load recent scripted runs: %w", err)
		}
		for id, flags := range fellBack {
			demoted[id] = demotedBy(flags)
		}
	}

	for i := range items {
		items[i].NextMode = modeFor(contents[i], items[i].Graduate, demoted[items[i].ID])
	}
	return nil
}
