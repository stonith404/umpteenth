package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

// Graduation criteria, decided on 2026-09-26
const (
	// graduationRuns is how many successful Assisted runs in a row a job needs before it may graduate
	graduationRuns = 3
	// maxAdHocBash is how many bash calls beyond reading files each of those runs may make
	maxAdHocBash = 2
)

// graduation is what reflection knows about the job's path to Scripted mode
type graduation struct {
	// Eligible means the job may get a main script with propose_main
	Eligible bool
	Reason   string
	// Sequence is the toolkit and MCP calls the qualifying runs made, in order
	Sequence []string
	Demoted  bool
	// Off means graduation is turned off for the job, so reflection may neither add nor repair a main script
	Off bool
	// FellBack is the reason this run's main script failed, when it did
	FellBack string
}

// runPattern is what one run did, reduced to what the graduation criteria compare
type runPattern struct {
	Number    int64
	Status    string
	Mode      string
	Sequence  []string
	AdHocBash int
}

// checkGraduation decides whether the job may graduate after this run
func (m *Module) checkGraduation(ctx context.Context, run reflectiondb.GetRunRow, runEvents []reflectiondb.RunEventsRow, current playbook.Content, demoted bool) (graduation, error) {
	g := graduation{Demoted: demoted, FellBack: fallbackReason(runEvents)}
	if current.Main != nil && !demoted {
		g.Reason = "the job already runs its main script"
		return g, nil
	}

	// Only the job's latest runs count, so learning from an older run can't graduate the job on history that later runs have moved past
	latest, err := m.queries.RecentRuns(ctx, reflectiondb.RecentRunsParams{WorkspaceID: run.WorkspaceID, JobID: run.JobID, Number: math.MaxInt64, MaxRuns: 1})
	if err != nil {
		return g, fmt.Errorf("failed to load the latest run: %w", err)
	}
	if len(latest) > 0 && latest[0].Number != run.Number {
		g.Reason = fmt.Sprintf("run #%d is newer, and graduation is judged on the latest runs", latest[0].Number)
		return g, nil
	}

	// The run itself and the ones before it must all qualify
	patterns := []runPattern{patternOf(run.Number, run.Status, run.Mode, runEvents)}
	previous, err := m.queries.RecentRuns(ctx, reflectiondb.RecentRunsParams{WorkspaceID: run.WorkspaceID, JobID: run.JobID, Number: run.Number, MaxRuns: graduationRuns - 1})
	if err != nil {
		return g, fmt.Errorf("failed to load earlier runs: %w", err)
	}
	for _, p := range previous {
		rows, err := m.queries.RunEvents(ctx, p.ID)
		if err != nil {
			return g, fmt.Errorf("failed to load the events of run #%d: %w", p.Number, err)
		}
		patterns = append(patterns, patternOf(p.Number, p.Status, p.Mode, rows))
	}

	g.Eligible, g.Reason = qualifies(patterns)
	if g.Eligible {
		g.Sequence = patterns[0].Sequence
	}
	return g, nil
}

// qualifies applies the graduation criteria to the latest runs, newest first
func qualifies(patterns []runPattern) (bool, string) {
	if len(patterns) < graduationRuns {
		return false, fmt.Sprintf("it needs %d successful Assisted runs in a row", graduationRuns)
	}
	for _, p := range patterns[:graduationRuns] {
		if p.Status != runner.StatusSucceeded || p.Mode != runner.ModeAssisted {
			return false, fmt.Sprintf("run #%d was a %s %s run, not a successful Assisted run", p.Number, p.Status, p.Mode)
		}
		if p.AdHocBash > maxAdHocBash {
			return false, fmt.Sprintf("run #%d made %d ad-hoc bash calls, at most %d are allowed", p.Number, p.AdHocBash, maxAdHocBash)
		}
		if !slices.Equal(p.Sequence, patterns[0].Sequence) {
			return false, fmt.Sprintf("runs #%d and #%d called different toolkit or MCP tools", patterns[0].Number, p.Number)
		}
	}
	return true, ""
}

// patternOf reduces a run's events to its toolkit and MCP call sequence and its ad-hoc bash calls
func patternOf(number int64, status, mode string, rows []reflectiondb.RunEventsRow) runPattern {
	p := runPattern{Number: number, Status: status, Mode: mode, Sequence: []string{}}
	for _, row := range rows {
		if row.Type != events.TypeToolCall {
			continue
		}
		var call toolCallPayload
		if json.Unmarshal([]byte(row.Payload), &call) != nil {
			continue
		}
		switch {
		case call.Name == "bash":
			if !readOnlyCommand(commandOf(call)) {
				p.AdHocBash++
			}
		case strings.Contains(call.Name, "__"):
			// Toolkit and MCP tools both use names with double-underscore separators
			p.Sequence = append(p.Sequence, call.Name)
		}
	}
	return p
}

// readOnlyCommands only look at files, which the criteria don't count as ad-hoc work
var readOnlyCommands = []string{"cat", "head", "tail", "ls", "grep", "rg", "jq", "wc", "find", "file", "stat", "echo", "printf", "pwd", "tree", "du", "diff"}

// readOnlyCommand reports whether every part of a shell command only reads, without redirecting output into a file
func readOnlyCommand(cmd string) bool {
	if strings.TrimSpace(cmd) == "" || strings.Contains(cmd, ">") {
		return false
	}
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for part := range strings.SplitSeq(replacer.Replace(cmd), "\n") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		if !slices.Contains(readOnlyCommands, fields[0]) {
			return false
		}
	}
	return true
}

// fallbackReason is why the run's main script handed over to the agent, or empty when it didn't
func fallbackReason(rows []reflectiondb.RunEventsRow) string {
	for _, row := range rows {
		if row.Type != events.TypeFallback {
			continue
		}
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(row.Payload), &p)
		if p.Reason == "" {
			return "the main script failed"
		}
		return p.Reason
	}
	return ""
}
