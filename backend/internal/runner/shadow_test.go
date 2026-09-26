//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	job := runner.JobConfig{ID: h.jobID, ModelID: "m1", BaseImage: "img", Limits: runner.Limits{TimeoutSeconds: 600}, OutputNames: []string{"count"}}
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
		require.ErrorIs(t, live.State.Set(ctx, "huge", strings.Repeat("a", 16<<20)), runner.ErrRecordLimit, "a shadow run holds a bounded amount of state in memory")
		cursor, _, _ = live.State.Get(ctx, "cursor")
		assert.Equal(t, "2", cursor)
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
	})

	source := runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Input: json.RawMessage(`{"count":2}`)}
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
		return runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Input: json.RawMessage(`{"count":2}`)}
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

func TestShadowRunStateCountsAgainstTheJobsLimits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mib := strings.Repeat("a", 1<<20)

	// The job's own state is one key short of its key limit and 1 MiB short of its size limit
	require.NoError(t, h.db.InTx(ctx, func(tx *database.Tx) error {
		for i := range runner.MaxStateKeys - 1 {
			value := "v"
			if i < 15 {
				value = mib
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO job_state (job_id, key, value, updated_at) VALUES ($1, $2, $3, 0)", h.jobID, fmt.Sprintf("k%03d", i), value)
			if err != nil {
				return err
			}
		}
		return nil
	}))

	// Writes a real run's store would refuse are refused in the shadow run too, while replacing the job's own keys works
	h.onShadowMain(t, "", 0, func(live *runner.LiveRun, _ *sandboxfake.Sandbox) {
		require.ErrorIs(t, live.State.Set(ctx, "new", mib), runner.ErrRecordLimit)
		require.NoError(t, live.State.Set(ctx, "k000", mib))
		require.NoError(t, live.State.Set(ctx, "k001", "small"))
		require.NoError(t, live.State.Set(ctx, "new", mib))
		require.ErrorIs(t, live.State.Set(ctx, "one-too-many", "v"), runner.ErrRecordLimit)
		require.NoError(t, live.State.Set(ctx, "new", "v"))
		require.NoError(t, live.SetOutput("count", json.RawMessage(`2`)))
	})
	source := runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Input: json.RawMessage(`{"count":2}`)}
	res, err := h.runner.Shadow(ctx, source, h.shadowJob(`{"checks":[],"llm":false}`))
	require.NoError(t, err)
	require.True(t, res.Passed, res.Reason+res.Inconclusive)

	// The job's own state is untouched
	var keys, size int64
	require.NoError(t, h.db.QueryRowContext(ctx, "SELECT COUNT(*), SUM(LENGTH(value)) FROM job_state WHERE job_id = $1", h.jobID).Scan(&keys, &size))
	assert.EqualValues(t, runner.MaxStateKeys-1, keys)
	assert.EqualValues(t, 15<<20+runner.MaxStateKeys-16, size)
}

func TestShadowRunCountsTheTokensOfItsModelCalls(t *testing.T) {
	h := newHarness(t)

	// Main makes a ump llm call, and the verifier judges what it reported
	h.onShadowMain(t, "", 0, func(l *runner.LiveRun, _ *sandboxfake.Sandbox) {
		settle, ok := l.ReserveSpend(5000)
		require.True(t, ok)
		settle(llm.Usage{Input: 300, Output: 30, CacheRead: 1000}, 1000)
		require.NoError(t, l.SetOutput("count", json.RawMessage(`2`)))
	})
	verdict, err := json.Marshal(map[string]any{"pass": true, "reason": "The count is there", "summary": ""})
	require.NoError(t, err)
	h.provider.Enqueue(fake.ScriptedResponse{Text: string(verdict), Usage: llm.Usage{Input: 2000, Output: 100}})

	source := runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Input: json.RawMessage(`{"count":2}`)}
	res, err := h.runner.Shadow(context.Background(), source, h.shadowJob(`{"checks":[],"llm":true}`))
	require.NoError(t, err)
	require.True(t, res.Passed, res.Reason+res.Inconclusive)

	// Tokens are input plus output, so the cache read counts toward the cost but not the tokens
	assert.EqualValues(t, 1000+7500, res.Cost)
	assert.EqualValues(t, 330+2100, res.Tokens)
}

func TestShadowRunCountsUmpLLMCallsMainLeftRunning(t *testing.T) {
	h := newHarness(t)

	// Main exits while a ump llm call it started is still running
	var live *runner.LiveRun
	h.onShadowMain(t, "", 0, func(l *runner.LiveRun, _ *sandboxfake.Sandbox) {
		live = l
		_, ok := l.ReserveSpend(5000)
		require.True(t, ok)
		require.NoError(t, l.SetOutput("count", json.RawMessage(`2`)))
	})

	source := runner.Run{ID: database.NewID(), WorkspaceID: h.wid, JobID: h.jobID, Input: json.RawMessage(`{"count":2}`)}
	res, err := h.runner.Shadow(context.Background(), source, h.shadowJob(`{"checks":[],"llm":false}`))
	require.NoError(t, err)
	require.True(t, res.Passed, res.Reason+res.Inconclusive)

	// The shadow run's cost counts the running call with its reservation, and no call may start after that
	assert.EqualValues(t, 5000, res.Cost)
	_, ok := live.ReserveSpend(0)
	assert.False(t, ok)
}
