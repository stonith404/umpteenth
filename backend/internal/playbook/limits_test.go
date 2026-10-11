//go:build unit

package playbook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

func TestParseLimitChange(t *testing.T) {
	l, err := playbook.ParseLimitChange(`{"memoryMb": 4096, "cpus": 1.5}`)
	require.NoError(t, err)
	assert.Equal(t, playbook.LimitChange{MemoryMB: new(4096), CPUs: new(1.5)}, l)

	// Every limit keeps to the bounds of the job form
	for raw, reason := range map[string]string{
		`{"memoryMb": 10}`:              "memoryMb must be between 64 and 262144",
		`{"memoryMb": 300000}`:          "memoryMb must be between 64 and 262144",
		`{"cpus": 0.05}`:                "cpus must be between 0.1 and 64",
		`{"cpus": 65}`:                  "cpus must be between 0.1 and 64",
		`{"timeoutSeconds": 10}`:        "timeoutSeconds must be between 30 and 86400",
		`{"timeoutSeconds": 90000}`:     "timeoutSeconds must be between 30 and 86400",
		`{"maxTurns": 0}`:               "maxTurns must be between 1 and 1000",
		`{"maxTurns": 1001}`:            "maxTurns must be between 1 and 1000",
		`{"memoryMb": 4096, "cpus": 0}`: "cpus must be between 0.1 and 64",
	} {
		_, err := playbook.ParseLimitChange(raw)
		require.EqualError(t, err, reason, raw)
	}

	// Content that sets no limit, sets one reflection may not suggest, or isn't one JSON object explains the shape it needs
	for _, raw := range []string{`{}`, `null`, `{"memoryMb": null}`, `{"maxCostUsd": 5}`, `{"memory": 4096}`, `4096`, `more memory`, `{"memoryMb": 4096} {}`, `{"memoryMb": "4096"}`} {
		_, err := playbook.ParseLimitChange(raw)
		require.ErrorContains(t, err, `such as {"memoryMb": 4096}`, raw)
	}
}

func TestLimitChangePickAndUnchanged(t *testing.T) {
	current := playbook.LimitChange{TimeoutSeconds: new(600), MaxTurns: new(40), CPUs: new(2.0), MemoryMB: new(1024)}
	change := playbook.LimitChange{MemoryMB: new(4096), MaxTurns: new(40)}

	// The values before are the job's values of exactly the limits the change sets
	assert.Equal(t, playbook.LimitChange{MemoryMB: new(1024), MaxTurns: new(40)}, change.Pick(current))

	// A limit the job already has is named, so reflection can be told to leave it out
	assert.Equal(t, []string{"maxTurns"}, change.Unchanged(current))
	assert.Empty(t, playbook.LimitChange{MemoryMB: new(4096)}.Unchanged(current))
}
