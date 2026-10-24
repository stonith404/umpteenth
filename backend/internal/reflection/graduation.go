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
	// graduationRuns is how many successful Assisted runs that did the job's work a job needs before it may graduate
	graduationRuns = 3
	// maxAdHocBash is how many bash calls beyond reading files each of those runs may make
	maxAdHocBash = 2
	// graduationWindow bounds how far back the streak is searched, since runs that found nothing to do are passed over
	graduationWindow = 30
	// maxReportedChars and maxReportedOutputs bound what each qualifying run reported in the reflection prompt
	maxReportedChars   = 1000
	maxReportedOutputs = 500
)

// graduation is what reflection knows about the job's path to Scripted mode
type graduation struct {
	// Eligible means the job may get a main script with propose_main
	Eligible bool
	Reason   string
	// Sequence is the toolkit and MCP calls the qualifying runs made, in order
	Sequence []string
	// Runs are the qualifying runs that did the job's work, newest first, with what they reported
	Runs []runPattern
	// Idle are the numbers of the runs between them that found nothing to do, a case main must handle too
	Idle []int64
	// Demotion is why repeated fallbacks or handovers took the job back to Assisted, empty while it isn't demoted
	Demotion string
	// Off means graduation is turned off for the job, so reflection may neither add nor repair a main script
	Off bool
	// FellBack is the reason this run's main script failed, when it did
	FellBack string
	// HandedOver is the reason this run's main script handed the run to the agent, when it did
	HandedOver string
}

// runPattern is what one run did, reduced to what the graduation criteria compare
type runPattern struct {
	Number    int64
	Status    string
	Mode      string
	Sequence  []string
	AdHocBash int
	// Idle means the agent finished the run saying it found nothing to do
	Idle bool
	// Instructed means the run had additional instructions for it alone, so it shows something else than the job as it is
	Instructed bool
	// Summary and Outputs are what the run reported, redacted and shortened for the prompt
	Summary, Outputs string
}

// checkGraduation decides whether the job may graduate after this run, with what the qualifying runs reported passed through redact
func (m *Module) checkGraduation(ctx context.Context, run reflectiondb.GetRunRow, runEvents []reflectiondb.RunEventsRow, current playbook.Content, demotion string, redact func(string) string) (graduation, error) {
	g := graduation{Demotion: demotion, FellBack: eventReason(runEvents, events.TypeFallback, "the main script failed"), HandedOver: eventReason(runEvents, events.TypeHandover, "the main script handed over")}
	if current.Main != nil && demotion == "" {
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

	// The run itself and the ones before it are judged newest first, and runs that found nothing to do are passed over, so the search may reach back past the streak
	ids := []string{run.ID}
	newest := patternOf(run.Number, run.Status, run.Mode, runEvents)
	newest.Instructed = run.Instructions != nil
	patterns := []runPattern{newest}
	ok, reason, done := qualifies(patterns)
	previous, err := m.queries.RecentRuns(ctx, reflectiondb.RecentRunsParams{WorkspaceID: run.WorkspaceID, JobID: run.JobID, Number: run.Number, MaxRuns: graduationWindow - 1})
	if err != nil {
		return g, fmt.Errorf("failed to load earlier runs: %w", err)
	}
	for _, p := range previous {
		if done {
			break
		}

		// Events are loaded one run at a time, since most jobs settle the question within two runs
		rows, err := m.queries.RunEvents(ctx, p.ID)
		if err != nil {
			return g, fmt.Errorf("failed to load the events of run #%d: %w", p.Number, err)
		}
		ids = append(ids, p.ID)
		pattern := patternOf(p.Number, p.Status, p.Mode, rows)
		pattern.Instructed = p.Instructions != nil
		patterns = append(patterns, pattern)
		ok, reason, done = qualifies(patterns)
	}
	g.Eligible, g.Reason = ok, reason
	if !g.Eligible {
		return g, nil
	}

	// The streak ends at its third working run, so every run loaded belongs to it
	g.Sequence = patterns[0].Sequence
	for i, p := range patterns {
		// A run with additional instructions was passed over like an idle one, but shows neither the work nor the quiet path
		if p.Instructed {
			continue
		}
		if p.Idle {
			g.Idle = append(g.Idle, p.Number)
			continue
		}

		// What the working runs reported shows main what to produce, and only these few runs are loaded whole
		reported := run
		if i > 0 {
			reported, err = m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: run.WorkspaceID, ID: ids[i]})
			if err != nil {
				return g, fmt.Errorf("failed to load run #%d: %w", p.Number, err)
			}
		}

		// Reports are redacted before they are shortened, since a cut through a secret would leave the part before the cut unredacted
		p.Summary = truncated(redact(derefString(reported.Summary)), maxReportedChars)
		p.Outputs = truncated(redact(derefString(reported.Outputs)), maxReportedOutputs)
		g.Runs = append(g.Runs, p)
	}
	return g, nil
}

// qualifies applies the graduation criteria to the latest runs, newest first, and done reports that older runs can't change the outcome
// Runs that found nothing to do neither count toward the streak nor break it, since they never reached the steps main must take
// Runs with additional instructions for them alone are passed over the same way, since they did something else than the job as it is
func qualifies(patterns []runPattern) (ok bool, reason string, done bool) {
	// Main is written from the newest run's transcript, so that run must have done the work, and done it as the job is
	if len(patterns) > 0 && patterns[0].Idle {
		return false, fmt.Sprintf("run #%d found nothing to do, and a job only graduates after a run that did its work", patterns[0].Number), true
	}
	if len(patterns) > 0 && patterns[0].Instructed {
		return false, fmt.Sprintf("run #%d had additional instructions for that run alone, and a job only graduates after a run that did the job as it is", patterns[0].Number), true
	}
	// Working runs count until the streak is complete, and anything but a successful Assisted run ends the search
	working, idle := 0, 0
	for _, p := range patterns {
		if p.Status != runner.StatusSucceeded || p.Mode != runner.ModeAssisted {
			return false, fmt.Sprintf("run #%d was a %s %s run, not a successful Assisted run", p.Number, p.Status, p.Mode), true
		}
		if p.Idle {
			idle++
			continue
		}
		if p.Instructed {
			continue
		}
		if p.AdHocBash > maxAdHocBash {
			return false, fmt.Sprintf("run #%d made %d ad-hoc bash calls, at most %d are allowed", p.Number, p.AdHocBash, maxAdHocBash), true
		}
		if !slices.Equal(p.Sequence, patterns[0].Sequence) {
			return false, fmt.Sprintf("runs #%d and #%d called different toolkit or MCP tools", patterns[0].Number, p.Number), true
		}
		working++
		if working == graduationRuns {
			return true, "", true
		}
	}

	// The runs so far fall short, so older runs may still complete the streak
	if idle > 0 {
		return false, fmt.Sprintf("it needs %d successful Assisted runs that did the job's work, and %d of its latest runs found nothing to do, which don't count", graduationRuns, idle), false
	}
	return false, fmt.Sprintf("it needs %d successful Assisted runs in a row", graduationRuns), false
}

// patternOf reduces a run's events to its toolkit and MCP call sequence, its ad-hoc bash calls and whether the agent found nothing to do
func patternOf(number int64, status, mode string, rows []reflectiondb.RunEventsRow) runPattern {
	p := runPattern{Number: number, Status: status, Mode: mode, Sequence: []string{}}
	for _, row := range rows {
		switch row.Type {
		case events.TypeFinish:
			// A run without the flag counts as one that did the work
			var f struct {
				NothingToDo bool `json:"nothingToDo"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &f)
			p.Idle = f.NothingToDo
		case events.TypeToolCall:
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

// eventReason is the reason of the run's first event of a type, unnamed when the event gives none, or empty when there is no such event
func eventReason(rows []reflectiondb.RunEventsRow, typ, unnamed string) string {
	for _, row := range rows {
		if row.Type != typ {
			continue
		}
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(row.Payload), &p)
		if p.Reason == "" {
			return unnamed
		}
		return p.Reason
	}
	return ""
}
