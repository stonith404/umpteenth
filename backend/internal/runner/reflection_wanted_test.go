//go:build unit

package runner_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stonith404/umpteenth/backend/internal/runner"
)

func TestReflectionWanted(t *testing.T) {
	explore := runner.Run{Mode: "explore"}
	scripted := runner.Run{Mode: "scripted"}
	cases := []struct {
		name        string
		selfImprove bool
		run         runner.Run
		final       runner.Final
		want        bool
	}{
		{"learning is off", false, explore, runner.Final{Status: runner.StatusSucceeded, Turns: 3}, false},
		{"explore run succeeded", true, explore, runner.Final{Status: runner.StatusSucceeded, Turns: 3}, true},
		{"run failed", true, explore, runner.Final{Status: runner.StatusFailed, Turns: 3}, true},
		{"run was cancelled", true, explore, runner.Final{Status: runner.StatusCancelled, Turns: 3}, false},
		{"agent never started", true, explore, runner.Final{Status: runner.StatusFailed, Error: "Failed to create the sandbox"}, false},
		{"setup script broke the run", true, explore, runner.Final{Status: runner.StatusFailed, Error: "The setup script failed with exit code 1"}, true},
		{"successful scripted run", true, scripted, runner.Final{Status: runner.StatusSucceeded, Turns: 1}, false},
		{"scripted run with notes", true, scripted, runner.Final{Status: runner.StatusSucceeded, Turns: 1, Notes: []string{"x"}}, true},
		{"scripted run fell back", true, scripted, runner.Final{Status: runner.StatusSucceeded, Turns: 2, FellBack: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runner.ReflectionWanted(tc.selfImprove, tc.run, tc.final))
		})
	}
}
