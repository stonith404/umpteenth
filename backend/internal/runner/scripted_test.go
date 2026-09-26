//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// graduate gives the harness job a main script and verify checks, and queues a scripted run of it
func (h *harness) graduate(t *testing.T, verify string) string {
	t.Helper()
	main := "#!/usr/bin/env bash\n# ump:description Report the stories\ncurl -s api\n"
	c := playbook.Content{Main: &main, Verify: json.RawMessage(verify)}
	_, err := playbook.New(playbook.Dependencies{DB: h.db}).Save(context.Background(), h.wid, h.jobID, c, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)
	id := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'queued', 'scripted', 'manual', 1, $4)`,
		id, h.wid, h.jobID, database.Now())
	return id
}

// onMain answers the run's main script, which may report outputs and steps through the live run like ump would through the broker
func (h *harness) onMain(runID string, stdout string, exitCode int, script func(live *runner.LiveRun)) {
	h.adapter.OnFunc(func(cmd []string) bool {
		return len(cmd) > 3 && cmd[3] == "ump-toolkit" && strings.Contains(cmd[2], "/ump/main")
	},
		func(_ context.Context, _ *sandboxfake.Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			if live, ok := h.live.Get(runID); ok && script != nil {
				script(live)
			}
			_, _ = io.WriteString(req.Stdout, stdout)
			return sandbox.ExecResult{ExitCode: exitCode}, nil
		})
}

func (h *harness) eventTypes(t *testing.T, runID string) []string {
	rows, err := h.db.QueryContext(context.Background(), "SELECT type FROM run_events WHERE run_id = $1 ORDER BY seq", runID)
	require.NoError(t, err)
	defer rows.Close()
	var types []string
	for rows.Next() {
		var typ string
		require.NoError(t, rows.Scan(&typ))
		types = append(types, typ)
	}
	return types
}

func (h *harness) fellBack(t *testing.T, runID string) bool {
	var fellBack bool
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT fell_back FROM runs WHERE id = $1", runID).Scan(&fellBack))
	return fellBack
}

func TestScriptedRunPassesItsChecksWithoutTheAgent(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":["output.count > 0","stdout contains \"done\""],"llm":false}`)
	h.onMain(id, "fetched 2 stories, done\n", 0, func(live *runner.LiveRun) {
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
		require.NoError(t, live.SetSummary("Reported **2** stories"))
	})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// The run succeeded on the script alone: no model call, no fallback
	status, errMsg, summary, outputs, cost := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.Equal(t, "Reported **2** stories", summary)
	assert.JSONEq(t, `{"count":2}`, outputs)
	assert.Zero(t, cost)
	assert.Empty(t, h.provider.Requests())
	assert.False(t, h.fellBack(t, id))
	assert.Contains(t, h.eventTypes(t, id), "verify.result")
	assert.NotContains(t, h.eventTypes(t, id), "fallback")
	require.Len(t, h.finals, 1)
	assert.False(t, h.finals[0].FellBack)
}

func TestScriptedRunFallsBackToTheAgentInTheSameSandbox(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":["output.count > 0"],"llm":false}`)

	// The script posted a message, then broke on a renamed input field
	h.onMain(id, "Traceback: KeyError: 'amount'\n", 1, func(live *runner.LiveRun) {
		require.NoError(t, live.AddStep("post digest"))
		live.RecordAction("MCP tool slack.post_message with {\"text\":\"digest\"} (succeeded)")
	})
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{
		"status": "success", "summary": "Finished what the script started", "outputs": map[string]any{"count": 2},
	}}}, Usage: llm.Usage{Input: 1000, Output: 100}})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// The agent finished the job and the run is marked as fallen back
	status, errMsg, summary, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.Equal(t, "Finished what the script started", summary)
	assert.True(t, h.fellBack(t, id))
	types := h.eventTypes(t, id)
	assert.Less(t, slices.Index(types, "verify.result"), slices.Index(types, "fallback"))
	require.Len(t, h.finals, 1)
	assert.True(t, h.finals[0].FellBack)

	// The agent was told why the script failed, where it stopped, and what must not happen twice
	requests := h.provider.Requests()
	require.Len(t, requests, 1)
	brief := requests[0].Messages[0].Text()
	assert.Contains(t, brief, `the check "exit_code == 0" failed: exit code was 1`)
	assert.Contains(t, brief, `The last step the script reported was "post digest"`)
	assert.Contains(t, brief, "KeyError: 'amount'")
	assert.Contains(t, brief, "Do not repeat them")
	assert.Contains(t, brief, "slack.post_message")
}

func TestScriptedRunIsJudgedWhenVerifyAsksForIt(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":[],"llm":true}`)
	h.onMain(id, "Top stories: (none)\n", 0, nil)

	// The checks pass, but the utility model finds the result wrong, so the agent takes over
	verdict, err := json.Marshal(map[string]any{"pass": false, "reason": "The digest lists no stories", "summary": ""})
	require.NoError(t, err)
	h.provider.Enqueue(
		fake.ScriptedResponse{Text: string(verdict), Usage: llm.Usage{Input: 2000, Output: 100}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "Listed the stories"}}}},
	)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.True(t, h.fellBack(t, id))
	var verifyCost int64
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT verify_cost FROM runs WHERE id = $1", id).Scan(&verifyCost))
	// 2000 input tokens at $3/M plus 100 output tokens at $15/M
	assert.EqualValues(t, 7500, verifyCost)
	assert.Contains(t, h.provider.Requests()[1].Messages[0].Text(), "The digest lists no stories")
}

func (h *harness) reflection(t *testing.T, runID string) (string, bool) {
	var state string
	var requestedAt *int64
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT reflection, reflection_requested_at FROM runs WHERE id = $1", runID).Scan(&state, &requestedAt))
	return state, requestedAt != nil
}

func TestFinishedRunsOfALearningJobAreMarkedForReflectionAtOnce(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, "UPDATE jobs SET self_improve = TRUE WHERE id = $1", h.jobID)

	// A successful scripted run has nothing to teach, so it is skipped
	id := h.graduate(t, `{"checks":[],"llm":false}`)
	h.onMain(id, "ok\n", 0, nil)
	require.NoError(t, h.runner.Execute(context.Background(), id))
	state, requested := h.reflection(t, id)
	assert.Equal(t, runner.ReflectionSkipped, state)
	assert.False(t, requested)

	// An agent run is marked pending together with its final status, before anyone is notified
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}})
	agentRun := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 2, 'queued', 'assisted', 'manual', 1, $4)`,
		agentRun, h.wid, h.jobID, database.Now())
	require.NoError(t, h.runner.Execute(context.Background(), agentRun))
	state, requested = h.reflection(t, agentRun)
	assert.Equal(t, runner.ReflectionPending, state)
	assert.True(t, requested)
	require.Len(t, h.finals, 2)
	assert.Equal(t, runner.ReflectionPending, h.finals[1].Reflection)
}

func TestScriptedRunChecksOutputsWhoseNamesHoldSpaces(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, `UPDATE jobs SET spec = $1 WHERE id = $2`, `{"outputs":[{"name":"Top story","type":"string"}]}`, h.jobID)
	id := h.graduate(t, `{"checks":[],"llm":false}`)
	h.onMain(id, "done\n", 0, func(live *runner.LiveRun) {
		require.NoError(t, live.SetOutput("Top story", json.RawMessage(`"Rust 2.0"`)))
	})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// The declared output is found by its name, so the run passes without falling back
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.False(t, h.fellBack(t, id))
}

func TestFallbackSuccessOutlivesTheScriptsUmpFail(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":[],"llm":false}`)

	// The script gives up through ump fail, which exits 1, and the agent then finishes the job
	h.onMain(id, "", 1, func(live *runner.LiveRun) { live.Fail("the API answered 500") })
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "Done by the agent"}}}})
	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.True(t, h.fellBack(t, id))

	// A script that reports a failure and still exits 0 doesn't pass either
	h2 := newHarness(t)
	id2 := h2.graduate(t, `{"checks":[],"llm":false}`)
	h2.onMain(id2, "", 0, func(live *runner.LiveRun) { live.Fail("the API answered 500") })
	h2.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "Done by the agent"}}}})
	require.NoError(t, h2.runner.Execute(context.Background(), id2))
	assert.True(t, h2.fellBack(t, id2))
}
