package reflection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

// compareWindow is how recent a run must be for a shadow run to compare outputs with it, since older outputs say little about what main finds today
const compareWindow = time.Hour

// MainTester runs a proposed main script in a shadow run before reflection applies it
type MainTester interface {
	Shadow(ctx context.Context, run runner.Run, job runner.JobConfig) (runner.ShadowResult, error)
}

// SetMainTester wires the runner, which only exists after reflection because it tells reflection about finished runs
func (m *Module) SetMainTester(t MainTester) {
	m.tester = t
}

// mainOps change what a graduated job's main script does, so they are tried out and rejected together
var mainOps = []string{playbook.OpProposeMain, playbook.OpUpdateMain, playbook.OpSetVerify, playbook.OpUpsertScript, playbook.OpDeleteScript, playbook.OpSetSetup, playbook.OpSetDockerfile}

// verifyMain tries a main script that reflection added or changed, directly or through what it depends on, in a shadow run before the change is applied
// A main that fails is rejected with what went wrong, so the model can fix it in its second attempt instead of the next real run falling back
func (m *Module) verifyMain(ctx context.Context, run reflectiondb.GetRunRow, runEvents []reflectiondb.RunEventsRow, job runner.JobConfig, current playbook.Content, next *playbook.Content, results []playbook.AppliedOp, res *result) error {
	if next.Main == nil || !mainChanged(current, *next) {
		return nil
	}
	primary := primaryMainOp(results)
	if primary < 0 {
		return nil
	}

	// A job that may change something elsewhere is never shadowed, since main's own requests can't be held back
	if skip := m.shadowSkipReason(run, runEvents, job, *next); skip != "" {
		results[primary].Test = &playbook.OpTest{Status: playbook.TestSkipped, Detail: capitalize(skip)}
		return nil
	}

	// Run main with the input of the run it was learned from
	shadowJob := job
	err := shadowJob.ApplyPlaybook(*next)
	if err != nil {
		return err
	}
	shadow, err := m.tester.Shadow(ctx, runner.Run{WorkspaceID: run.WorkspaceID, JobID: run.JobID, Input: json.RawMessage(derefString(run.Input))}, shadowJob)
	res.Cost += shadow.Cost
	res.Tokens += shadow.Tokens

	// A shutdown says nothing about main, so the reflection is retried instead
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case err != nil:
		results[primary].Test = &playbook.OpTest{Status: playbook.TestSkipped, Detail: "The shadow run could not run: " + truncated(err.Error(), 500)}
		return nil
	case shadow.Inconclusive != "":
		results[primary].Test = &playbook.OpTest{Status: playbook.TestSkipped, Detail: "The shadow run was inconclusive: " + truncated(shadow.Inconclusive, 1000)}
		return nil
	case !shadow.Passed:
		rejectMain(current, next, results, primary, shadow.Reason, shadow.Output)
		return nil
	}

	// Outputs that differ from the run's mean main does something else than the run did, which its checks may not notice
	verify, _ := playbook.ParseVerify(next.Verify)
	compared, why := comparableOutputs(run, job, verify)
	if len(compared) > 0 {
		for _, name := range job.OutputNames {
			want, ok := compared[name]
			if !ok {
				continue
			}
			got := shadow.Outputs[name]
			if !playbook.SameValue(got, want) {
				reason := fmt.Sprintf("main reported the output %s as %s, but run #%d reported %s", name, clipValue(got), run.Number, clipValue(want))
				rejectMain(current, next, results, primary, reason, shadow.Output)
				return nil
			}
		}
		results[primary].Test = &playbook.OpTest{Status: playbook.TestPassed, Detail: fmt.Sprintf("Main passed its checks in a shadow run with the input of run #%d, and its outputs matched the run's.", run.Number)}
		return nil
	}
	results[primary].Test = &playbook.OpTest{Status: playbook.TestPassed, Detail: fmt.Sprintf("Main passed its checks in a shadow run with the input of run #%d. Its outputs were not compared, since %s.", run.Number, why)}
	return nil
}

// shadowSkipReason explains why main can't be tried in a shadow run, or returns an empty string when it can
func (m *Module) shadowSkipReason(run reflectiondb.GetRunRow, runEvents []reflectiondb.RunEventsRow, job runner.JobConfig, next playbook.Content) string {
	if m.tester == nil {
		return "no sandbox backend is available for a shadow run"
	}
	if len(job.SideEffects) > 0 {
		return fmt.Sprintf("the job has side effects (%s) that a shadow run could repeat", strings.Join(job.SideEffects, "; "))
	}
	header, err := playbook.ParseScriptHeader(*next.Main)
	if err == nil && header.SideEffects {
		return "main is marked with external side effects"
	}
	for _, s := range next.Toolkit {
		if s.SideEffects {
			return fmt.Sprintf("the toolkit script %s has external side effects", s.Name)
		}
	}

	// Main would start from the state the run left behind, e.g. with the new items already marked as seen
	if wroteState(runEvents) {
		return fmt.Sprintf("run #%d changed the job's state, so a shadow run wouldn't start where the run did", run.Number)
	}
	return ""
}

// comparableOutputs returns the run's declared outputs a shadow run must reproduce, or why there are none
func comparableOutputs(run reflectiondb.GetRunRow, job runner.JobConfig, verify playbook.Verify) (map[string]json.RawMessage, string) {
	if len(job.OutputNames) == 0 {
		return nil, "the job declares no outputs"
	}
	if run.Status != runner.StatusSucceeded {
		return nil, fmt.Sprintf("run #%d did not succeed", run.Number)
	}
	if run.FinishedAt == nil || time.Since(time.UnixMilli(*run.FinishedAt)) > compareWindow {
		return nil, fmt.Sprintf("run #%d finished too long ago for its outputs to still hold", run.Number)
	}
	var reported map[string]json.RawMessage
	_ = json.Unmarshal([]byte(derefString(run.Outputs)), &reported)
	out := map[string]json.RawMessage{}
	for _, name := range job.OutputNames {
		value, ok := reported[name]
		if ok && string(value) != "null" && !slices.Contains(verify.Varies, name) {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil, fmt.Sprintf("run #%d reported none of the job's outputs that stay the same between runs", run.Number)
	}
	return out, ""
}

// rejectMain takes back every change to main and what it depends on, and tells the model what the shadow run showed
func rejectMain(current playbook.Content, next *playbook.Content, results []playbook.AppliedOp, primary int, reason, output string) {
	for i := range results {
		if results[i].Status != playbook.OpApplied || !slices.Contains(mainOps, results[i].Op.Op) {
			continue
		}
		results[i].Status = playbook.OpRejected
		results[i].Reason = "main failed its shadow run with these changes: " + reason
	}
	results[primary].Reason = "main failed its shadow run: " + reason
	results[primary].Test = &playbook.OpTest{Status: playbook.TestFailed, Detail: capitalize(reason), Output: output}
	next.Main, next.Verify, next.Toolkit, next.Setup, next.Dockerfile = current.Main, current.Verify, current.Toolkit, current.Setup, current.Dockerfile
}

// primaryMainOp is the applied operation a shadow run's outcome is shown on: the main script's own, or else the first change main depends on
func primaryMainOp(results []playbook.AppliedOp) int {
	first := -1
	for i, r := range results {
		if r.Status != playbook.OpApplied || !slices.Contains(mainOps, r.Op.Op) {
			continue
		}
		if r.Op.Op == playbook.OpProposeMain || r.Op.Op == playbook.OpUpdateMain {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

// mainChanged reports whether main or anything it runs with differs between two playbooks
func mainChanged(a, b playbook.Content) bool {
	sameScript := func(x, y playbook.Script) bool { return x.Name == y.Name && x.Content == y.Content }
	return !samePtr(a.Main, b.Main) || !bytes.Equal(a.Verify, b.Verify) || !samePtr(a.Setup, b.Setup) || !samePtr(a.Dockerfile, b.Dockerfile) ||
		!slices.EqualFunc(a.Toolkit, b.Toolkit, sameScript)
}

// wroteState reports whether a run changed the job's state, through the agent's state_set tool or ump state set
// It relies on the broker keeping a run's first state write on the timeline past the run's limit of broker calls
func wroteState(rows []reflectiondb.RunEventsRow) bool {
	for _, row := range rows {
		switch row.Type {
		case events.TypeToolCall:
			var call toolCallPayload
			if json.Unmarshal([]byte(row.Payload), &call) == nil && call.Name == "state_set" {
				return true
			}
		case events.TypeBrokerCall:
			var call struct {
				Endpoint string `json:"endpoint"`
			}
			if json.Unmarshal([]byte(row.Payload), &call) == nil && strings.HasPrefix(call.Endpoint, "PUT /v1/state/") {
				return true
			}
		}
	}
	return false
}

// clipValue shortens an output value for a rejection reason, which the model and the UI read
func clipValue(v json.RawMessage) string {
	if len(v) == 0 {
		return "nothing"
	}
	out, _ := agent.Truncate(string(v), 200, 0)
	return out
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
