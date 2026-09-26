//go:build unit

package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// brokenImages fails every image lookup, like a registry that is down or a Dockerfile that no longer builds
type brokenImages struct{}

func (brokenImages) ResolveImage(context.Context, runner.JobConfig, func()) (string, string, error) {
	return "", "", errors.New("failed to pull the job image: registry unreachable")
}

func TestRunThatFailedBeforeItsWorkStartedIsNotLearnedFrom(t *testing.T) {
	h := newHarnessWithImages(t, brokenImages{})
	testutil.Exec(t, h.db, "UPDATE jobs SET self_improve = TRUE WHERE id = $1", h.jobID)

	// A scripted run that never got its image never ran its main script, so it has nothing to learn from
	scriptedRun := h.graduate(t, `{"checks":[],"llm":false}`)
	require.NoError(t, h.runner.Execute(context.Background(), scriptedRun))
	status, errMsg, _, _, _ := h.run(t, scriptedRun)
	require.Equal(t, runner.StatusFailed, status)
	require.Contains(t, errMsg, "Environment build failed")
	require.Empty(t, h.adapter.Calls(), "no sandbox was created, so main never ran")
	state, requested := h.reflection(t, scriptedRun)
	assert.Equal(t, runner.ReflectionSkipped, state)
	assert.False(t, requested)

	// An agent run that failed the same way is skipped too, and both runs are treated alike
	agentRun := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 2, 'queued', 'explore', 'manual', 1, $4)`,
		agentRun, h.wid, h.jobID, database.Now())
	require.NoError(t, h.runner.Execute(context.Background(), agentRun))
	status, errMsg, _, _, _ = h.run(t, agentRun)
	require.Equal(t, runner.StatusFailed, status)
	require.Contains(t, errMsg, "Environment build failed")
	state, requested = h.reflection(t, agentRun)
	assert.Equal(t, runner.ReflectionSkipped, state)
	assert.False(t, requested)
}

func TestScriptedRunCutOffByItsTimeLimitIsStillLearnedFrom(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, `UPDATE jobs SET self_improve = TRUE, limits = '{"timeoutSeconds":1}' WHERE id = $1`, h.jobID)
	id := h.graduate(t, `{"checks":[],"llm":false}`)

	// Main outlives the run's time limit, so the run ends without an agent turn but after its work started
	h.adapter.OnFunc(func(cmd []string) bool {
		return len(cmd) > 3 && cmd[3] == "ump-toolkit" && strings.Contains(cmd[2], "/ump/main")
	},
		func(context.Context, *sandboxfake.Sandbox, sandbox.ExecRequest) (sandbox.ExecResult, error) {
			time.Sleep(1500 * time.Millisecond)
			return sandbox.ExecResult{ExitCode: 143}, nil
		})

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, _, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusTimedOut, status)
	assert.False(t, h.fellBack(t, id))
	state, requested := h.reflection(t, id)
	assert.Equal(t, runner.ReflectionPending, state)
	assert.True(t, requested)
}
