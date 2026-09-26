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
		name  string
		run   runner.Run
		final runner.Final
		want  bool
	}{
		{"learning is off", explore, runner.Final{Status: runner.StatusSucceeded, Turns: 3}, false},
		{"explore run succeeded", explore, runner.Final{SelfImprove: true, Status: runner.StatusSucceeded, Turns: 3}, true},
		{"run failed", explore, runner.Final{SelfImprove: true, Status: runner.StatusFailed, Turns: 3}, true},
		{"run was cancelled", explore, runner.Final{SelfImprove: true, Status: runner.StatusCancelled, Turns: 3}, false},
		{"agent never started", explore, runner.Final{SelfImprove: true, Status: runner.StatusFailed, Error: "Failed to create the sandbox"}, false},
		{"setup script broke the run", explore, runner.Final{SelfImprove: true, Status: runner.StatusFailed, SetupFailed: true}, true},
		{"successful scripted run", scripted, runner.Final{SelfImprove: true, Status: runner.StatusSucceeded, Turns: 1}, false},
		{"scripted run with notes", scripted, runner.Final{SelfImprove: true, Status: runner.StatusSucceeded, Turns: 1, Notes: []string{"x"}}, true},
		{"scripted run fell back", scripted, runner.Final{SelfImprove: true, Status: runner.StatusSucceeded, Turns: 2, FellBack: true}, true},
		{"main script timed out", scripted, runner.Final{SelfImprove: true, Status: runner.StatusTimedOut, MainRan: true}, true},
		{"scripted run failed before main started", scripted, runner.Final{SelfImprove: true, Status: runner.StatusFailed, Error: "Failed to create the sandbox"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runner.ReflectionWanted(tc.run, tc.final))
		})
	}
}
