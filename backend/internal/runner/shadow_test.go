//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// onShadowMain answers a shadow run's main script, which reaches its live run through the broker token like ump would
func (h *harness) onShadowMain(t *testing.T, stdout string, exitCode int, script func(live *runner.LiveRun, sb *sandboxfake.Sandbox)) {
	h.adapter.OnFunc(func(cmd []string) bool {
		return len(cmd) > 3 && cmd[3] == "ump-toolkit" && strings.Contains(cmd[2], "/ump/main")
	},
		func(_ context.Context, sb *sandboxfake.Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			live, ok := h.live.ShadowByToken(crypto.HashToken(sb.Spec().Broker.Token))
			require.True(t, ok, "the shadow run is registered under its broker token")
			if script != nil {
				script(live, sb)
			}
			_, _ = io.WriteString(req.Stdout, stdout)
			return sandbox.ExecResult{ExitCode: exitCode}, nil
		})
}

func (h *harness) shadowJob(verify string) runner.JobConfig {
	job := runner.JobConfig{ID: h.jobID, WorkspaceID: h.wid, ModelID: "m1", BaseImage: "img", Limits: runner.Limits{TimeoutSeconds: 600}, OutputNames: []string{"count"}}
	main := "#!/usr/bin/env bash\ncount=$(jq .count /ump/input.json)\nump output set count \"$count\"\n"
	_ = job.ApplyPlaybook(playbook.Content{Main: &main, Verify: json.RawMessage(verify)})
	return job
}

func TestShadowRunTriesMainWithoutLeavingATrace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	testutil.Exec(t, h.db, "INSERT INTO job_state (job_id, key, value, updated_at) VALUES ($1, 'cursor', '1', $2)", h.jobID, database.Now())

	// Main sees the run's input and the job's state, and moves the cursor like it would in a real run
	h.onShadowMain(t, "reported 2 stories\n", 0, func(live *runner.LiveRun, sb *sandboxfake.Sandbox) {
		assert.True(t, live.Shadow)
		input, _, _, _ := sb.File("/ump/input.json")
		assert.JSONEq(t, `{"count":2}`, string(input))
		cursor, _, err := live.State.Get(ctx, "cursor")
		require.NoError(t, err)
		assert.Equal(t, "1", cursor)
		require.NoError(t, live.State.Set(ctx, "cursor", "2"))
		cursor, _, _ = live.State.Get(ctx, "cursor")
		assert.Equal(t, "2", cursor)
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
	})

	source := runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Number: 4, Input: json.RawMessage(`{"count":2}`)}
	res, err := h.runner.Shadow(ctx, source, h.shadowJob(`{"checks":["output.count > 0"],"llm":false}`))
	require.NoError(t, err)
	require.True(t, res.Passed, res.Reason+res.Inconclusive)
	assert.JSONEq(t, `2`, string(res.Outputs["count"]))
	assert.Contains(t, res.Output, "reported 2 stories")

	// The sandbox is gone, the job's state is untouched and no run got a timeline entry
	sandboxes, err := h.adapter.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, sandboxes)
	var cursor string
	require.NoError(t, h.db.QueryRowContext(ctx, "SELECT value FROM job_state WHERE job_id = $1 AND key = 'cursor'", h.jobID).Scan(&cursor))
	assert.Equal(t, "1", cursor)
	var eventCount int
	require.NoError(t, h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_events").Scan(&eventCount))
	assert.Zero(t, eventCount)
	assert.Empty(t, h.provider.Requests())
}

func TestShadowRunReportsFailuresAndWhatItCantJudge(t *testing.T) {
	ctx := context.Background()
	source := func(h *harness) runner.Run {
		return runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Number: 4, Input: json.RawMessage(`{"count":2}`)}
	}

	// A main that doesn't report what the job needs fails like a scripted run would
	h := newHarness(t)
	h.onShadowMain(t, "boom\n", 0, nil)
	res, err := h.runner.Shadow(ctx, source(h), h.shadowJob(`{"checks":[],"llm":false}`))
	require.NoError(t, err)
	assert.False(t, res.Passed)
	assert.Contains(t, res.Reason, "output count was not set")
	assert.Contains(t, res.Output, "boom")

	// A main that exits non-zero fails, whatever it reported
	h = newHarness(t)
	h.onShadowMain(t, "", 2, func(live *runner.LiveRun, _ *sandboxfake.Sandbox) {
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
	})
	res, err = h.runner.Shadow(ctx, source(h), h.shadowJob(`{"checks":[],"llm":false}`))
	require.NoError(t, err)
	assert.Contains(t, res.Reason, `the check "exit_code == 0" failed`)

	// A call the broker had to refuse makes the shadow run say nothing about main, even when main carried on
	h = newHarness(t)
	h.onShadowMain(t, "", 0, func(live *runner.LiveRun, _ *sandboxfake.Sandbox) {
		live.Refuse("MCP tool slack.post_message with {}")
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
	})
	res, err = h.runner.Shadow(ctx, source(h), h.shadowJob(`{"checks":[],"llm":false}`))
	require.NoError(t, err)
	assert.False(t, res.Passed)
	assert.Contains(t, res.Inconclusive, "slack.post_message")

	// A job without a model can't run at all, which isn't main's fault
	h = newHarness(t)
	job := h.shadowJob(`{"checks":[],"llm":false}`)
	job.ModelID = ""
	res, err = h.runner.Shadow(ctx, source(h), job)
	require.NoError(t, err)
	assert.NotEmpty(t, res.Inconclusive)
}
