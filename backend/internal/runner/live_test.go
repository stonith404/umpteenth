//go:build unit

package runner_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/runner"
)

func TestSetOutputBoundsWhatARunHoldsInMemory(t *testing.T) {
	live := &runner.LiveRun{}
	big := json.RawMessage(`"` + strings.Repeat("a", 600_000) + `"`)

	// One large value fits, a second one would push the total past the limit
	require.NoError(t, live.SetOutput("a", big))
	require.ErrorIs(t, live.SetOutput("b", big), runner.ErrRecordLimit)

	// Replacing a value only counts its growth, so overwriting the same key keeps working
	require.NoError(t, live.SetOutput("a", big))
	require.NoError(t, live.SetOutput("a", json.RawMessage(`1`)))
	require.NoError(t, live.SetOutput("b", big))

	// The number of distinct keys is bounded too
	many := &runner.LiveRun{}
	for i := range 100 {
		require.NoError(t, many.SetOutput(fmt.Sprint(i), json.RawMessage(`1`)))
	}
	require.ErrorIs(t, many.SetOutput("one more", json.RawMessage(`1`)), runner.ErrRecordLimit)
	require.NoError(t, many.SetOutput("0", json.RawMessage(`2`)))
}

func TestAddStepBoundsWhatARunHoldsInMemory(t *testing.T) {
	live := &runner.LiveRun{}
	require.ErrorIs(t, live.AddStep(strings.Repeat("a", 201)), runner.ErrRecordLimit)
	for range 1000 {
		require.NoError(t, live.AddStep("step"))
	}
	require.ErrorIs(t, live.AddStep("step"), runner.ErrRecordLimit)
}
