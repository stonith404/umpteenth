//go:build unit

package runner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// waitingImages never finishes the build, so the run's context decides how the wait ends
type waitingImages struct {
	// onWait runs when the run starts waiting, e.g. to cancel it
	onWait func()
}

func (w waitingImages) ResolveImage(ctx context.Context, _ runner.JobConfig, onWait func()) (string, string, error) {
	onWait()
	if w.onWait != nil {
		w.onWait()
	}
	<-ctx.Done()
	return "", "", ctx.Err()
}

func TestImageWaitThatTimesOutIsATimeoutNotABuildFailure(t *testing.T) {
	h := newHarnessWithImages(t, waitingImages{})
	testutil.Exec(t, h.db, `UPDATE jobs SET limits = '{"timeoutSeconds":1}' WHERE id = $1`, h.jobID)
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	assert.Equal(t, runner.StatusTimedOut, status)
	assert.NotContains(t, errMsg, "Environment build failed")
	assert.Empty(t, h.adapter.Calls(), "no sandbox is created for a run that never got its image")
}

func TestImageWaitThatIsStoppedIsCancelledNotABuildFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarnessWithImages(t, waitingImages{onWait: cancel})
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(ctx, id))
	status, errMsg, _, _, _ := h.run(t, id)
	assert.Equal(t, runner.StatusCancelled, status)
	assert.NotContains(t, errMsg, "Environment build failed")
}
