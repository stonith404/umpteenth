//go:build unit

package runner_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

func TestReserveSpendCountsTheAgentAndUmpLLM(t *testing.T) {
	live := &runner.LiveRun{Job: runner.JobConfig{Limits: runner.Limits{MaxCost: 1000}}}

	// Neither kind of spend alone reaches the limit, together they do
	live.AddAgentCost(600)
	settle, ok := live.ReserveSpend(100)
	require.True(t, ok)
	settle(llm.Usage{}, 400)
	_, ok = live.ReserveSpend(0)
	require.False(t, ok)

	// Without a limit there is always budget left
	unlimited := &runner.LiveRun{}
	unlimited.AddAgentCost(1 << 40)
	_, ok = unlimited.ReserveSpend(1 << 40)
	require.True(t, ok)
}

func TestReserveSpendBoundsParallelCalls(t *testing.T) {
	live := &runner.LiveRun{Job: runner.JobConfig{Limits: runner.Limits{MaxCost: 1000}}}

	// Parallel calls start only while their worst cases fit the budget together
	first, ok := live.ReserveSpend(400)
	require.True(t, ok)
	second, ok := live.ReserveSpend(400)
	require.True(t, ok)
	_, ok = live.ReserveSpend(400)
	require.False(t, ok)

	// A settled call frees its reservation and keeps only what it actually cost
	first(llm.Usage{Output: 10}, 100)
	third, ok := live.ReserveSpend(400)
	require.True(t, ok)
	second(llm.Usage{}, 100)
	third(llm.Usage{}, 100)
	usage, cost := live.Spend()
	require.EqualValues(t, 300, cost)
	require.EqualValues(t, 10, usage.Output)

	// A call larger than the rest of the budget may run alone, but nothing may join it
	alone, ok := live.ReserveSpend(5000)
	require.True(t, ok)
	_, ok = live.ReserveSpend(1)
	require.False(t, ok)
	alone(llm.Usage{}, 800)
	_, ok = live.ReserveSpend(0)
	require.False(t, ok)
}

func TestFinalSpendCountsCallsStillInFlight(t *testing.T) {
	live := &runner.LiveRun{Job: runner.JobConfig{Limits: runner.Limits{MaxCost: 1000}}}

	// One call settled and another is still running when the run's cost is taken for its record
	done, ok := live.ReserveSpend(400)
	require.True(t, ok)
	done(llm.Usage{Output: 10}, 100)
	running, ok := live.ReserveSpend(400)
	require.True(t, ok)

	// The running call is billed after the record is written, so the record counts it with its reservation
	usage, cost := live.FinalSpend()
	require.EqualValues(t, 500, cost)
	require.EqualValues(t, 10, usage.Output)

	// A call starting after that would count nowhere, so none may start, whatever the limit
	_, ok = live.ReserveSpend(0)
	require.False(t, ok)
	unlimited := &runner.LiveRun{}
	unlimited.FinalSpend()
	_, ok = unlimited.ReserveSpend(0)
	require.False(t, ok)
	running(llm.Usage{Output: 20}, 300)
}
