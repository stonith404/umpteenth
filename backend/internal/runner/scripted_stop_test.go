//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// statuses lists the statuses the run's timeline reported, in order
func (h *harness) statuses(t *testing.T, runID string) []string {
	rows, err := h.db.QueryContext(context.Background(), "SELECT payload FROM run_events WHERE run_id = $1 AND type = 'run.status' ORDER BY seq", runID)
	require.NoError(t, err)
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var raw string
		require.NoError(t, rows.Scan(&raw))
		var payload struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &payload))
		statuses = append(statuses, payload.Status)
	}
	return statuses
}

// assertStoppedWithoutFallback checks that a scripted run stopped during verification ends the way it was stopped, not as a fallback to the agent
// modelCalls is how many model calls verification made, since the agent that takes over after a fallback would make more
func (h *harness) assertStoppedWithoutFallback(t *testing.T, id, wantStatus string, modelCalls int) {
	t.Helper()
	status, _, _, _, _ := h.run(t, id)
	types, statuses := h.eventTypes(t, id), h.statuses(t, id)
	require.Len(t, h.finals, 1)
	assert.Equal(t, wantStatus, status)

	// Nothing about the script failed, the run was stopped, so it must not look like the script fell short and an agent took over
	assert.False(t, h.fellBack(t, id), "fell_back must stay false in the database")
	assert.False(t, h.finals[0].FellBack, "the final result must not report a fallback")
	assert.NotContains(t, types, "fallback")
	assert.NotContains(t, statuses[slices.Index(statuses, runner.StatusVerifying)+1:], runner.StatusRunning, "the run must not go back to running after verifying")
	assert.Len(t, h.provider.Requests(), modelCalls, "no agent may be started")
}

func TestScriptedRunCancelledWhileCheckingDoesNotFallBack(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":["file /workspace/report.txt exists"],"llm":false}`)
	h.onMain(id, "done\n", 0, nil)

	// The user cancels while the file check runs in the sandbox
	h.adapter.OnFunc(func(cmd []string) bool { return len(cmd) > 0 && cmd[0] == "test" }, func(ctx context.Context, _ *sandboxfake.Sandbox, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
		require.NoError(t, h.runs.Cancel(context.Background(), h.wid, id))
		<-ctx.Done()
		return sandbox.ExecResult{}, ctx.Err()
	})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	h.assertStoppedWithoutFallback(t, id, runner.StatusCancelled, 0)
}

func TestScriptedRunCancelledWhileJudgedDoesNotFallBack(t *testing.T) {
	h := newHarness(t)
	id := h.graduate(t, `{"checks":[],"llm":true}`)
	h.onMain(id, "Top stories: Rust 2.0\n", 0, nil)

	// The verifier takes a while to answer
	verdict, err := json.Marshal(map[string]any{"pass": true, "reason": "Lists the stories", "summary": "Listed the stories"})
	require.NoError(t, err)
	h.provider.Enqueue(fake.ScriptedResponse{Text: string(verdict), Delay: time.Minute})

	// The user cancels while the run shows verifying and the verifier is thinking
	go func() {
		for range 1000 {
			var status string
			if h.db.QueryRowContext(context.Background(), "SELECT status FROM runs WHERE id = $1", id).Scan(&status) == nil && status == runner.StatusVerifying && len(h.provider.Requests()) == 1 {
				assert.NoError(t, h.runs.Cancel(context.Background(), h.wid, id))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	require.NoError(t, h.runner.Execute(context.Background(), id))

	h.assertStoppedWithoutFallback(t, id, runner.StatusCancelled, 1)
}

func TestScriptedRunTimingOutWhileCheckingDoesNotFallBack(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, `UPDATE jobs SET self_improve = TRUE, limits = '{"timeoutSeconds":1}' WHERE id = $1`, h.jobID)
	id := h.graduate(t, `{"checks":["file /workspace/report.txt exists"],"llm":false}`)
	h.onMain(id, "done\n", 0, nil)

	// The file check is still running when the run reaches its time limit
	h.adapter.OnFunc(func(cmd []string) bool { return len(cmd) > 0 && cmd[0] == "test" }, func(ctx context.Context, _ *sandboxfake.Sandbox, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
		<-ctx.Done()
		return sandbox.ExecResult{}, ctx.Err()
	})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// A timed out run that fell back would count toward demotion and ask reflection to repair a main script that did not fail
	h.assertStoppedWithoutFallback(t, id, runner.StatusTimedOut, 0)
}
