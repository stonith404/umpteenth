//go:build unit

package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

func TestSystemBlocksWithoutSkillsAreUnchanged(t *testing.T) {
	// A job without skills keeps the blocks it had before skills existed, so its prompt cache keeps hitting
	blocks := SystemBlocks(PromptInput{Instruction: "Do it"})
	require.Equal(t, []llm.Block{{Text: baseInstructions, CacheBreakpoint: true}, {Text: "## The job\nDo it\n" + limitsSection(PromptLimits{}, false), CacheBreakpoint: true}}, blocks)

	blocks = SystemBlocks(PromptInput{Instruction: "Do it", Playbook: "Learned"})
	require.Len(t, blocks, 3)
	require.False(t, blocks[1].CacheBreakpoint)
	require.True(t, blocks[2].CacheBreakpoint)
}

func TestSystemBlocksListSkillsBeforeThePlaybook(t *testing.T) {
	skills := []PromptSkill{
		{Name: "alpha", Description: "Does A\n  with care", Path: "/ump/skills/alpha/SKILL.md"},
		{Name: "beta", Description: "Does B", Path: "/ump/skills/beta/SKILL.md"},
	}

	// Without a playbook, the skills block ends the cached prefix
	blocks := SystemBlocks(PromptInput{Instruction: "Do it", Skills: skills})
	require.Len(t, blocks, 3)
	require.False(t, blocks[1].CacheBreakpoint)
	require.True(t, blocks[2].CacheBreakpoint)
	require.Equal(t, skillsIntro+"- alpha: Does A with care (/ump/skills/alpha/SKILL.md)\n- beta: Does B (/ump/skills/beta/SKILL.md)\n", blocks[2].Text)

	// The playbook changes more often, so it comes after the skills and takes the breakpoint
	blocks = SystemBlocks(PromptInput{Instruction: "Do it", Skills: skills, Playbook: "Learned"})
	require.Len(t, blocks, 4)
	require.False(t, blocks[2].CacheBreakpoint)
	require.True(t, blocks[3].CacheBreakpoint)
	require.Equal(t, "## What earlier runs learned\nLearned", blocks[3].Text)
}

func TestSystemBlocksStateTheLimitsInTheJobBlock(t *testing.T) {
	limits := PromptLimits{Timeout: 90 * time.Minute, MaxTurns: 60, MaxCost: 2_000_000, CPUs: 0.5, MemoryMB: 1024}
	blocks := SystemBlocks(PromptInput{Instruction: "Do it", Limits: limits})

	// The limits never reach the static block, so editing them leaves the shared prefix cached
	require.Equal(t, baseInstructions, blocks[0].Text)
	require.True(t, blocks[1].CacheBreakpoint)
	require.Contains(t, blocks[1].Text, "\n## Limits\n- Time: 1 hour 30 minutes for the whole run.")
	require.Contains(t, blocks[1].Text, "- Turns: 60 model calls, the one that calls finish included.")
	require.Contains(t, blocks[1].Text, "- Model cost: $2.00")
	require.Contains(t, blocks[1].Text, "- CPUs: 0.5,")
	require.Contains(t, blocks[1].Text, "- Memory: 1024 MB for all processes")
	require.Contains(t, blocks[1].Text, "- User: not root, so you can't install system packages.")

	// A cost limit of zero is off, so the prompt doesn't mention it
	limits.MaxCost = 0
	blocks = SystemBlocks(PromptInput{Instruction: "Do it", Limits: limits})
	require.NotContains(t, blocks[1].Text, "Model cost")

	// A job that runs as root learns that it may install system packages, while the static block stays the same
	blocks = SystemBlocks(PromptInput{Instruction: "Do it", Limits: limits, RunAsRoot: true})
	require.Equal(t, baseInstructions, blocks[0].Text)
	require.Contains(t, blocks[1].Text, "- User: root, so you can install system packages.")
	require.NotContains(t, blocks[1].Text, "not root")
}

func TestBaseInstructionsEndWithOwnerNotes(t *testing.T) {
	// Reflection finds what the owner has to act on by this heading, so the agent must be told to use it
	require.Contains(t, baseInstructions, OwnerNotesHeading)
	require.Contains(t, limitsSection(PromptLimits{}, false), OwnerNotesHeading)
}

func TestSuccessCriteriaDescribeTheEndState(t *testing.T) {
	blocks := SystemBlocks(PromptInput{Instruction: "Do it", SuccessCriteria: []string{"The digest was posted"}})
	require.Contains(t, blocks[1].Text, "\n## Success criteria\nThey describe the end state a successful run leaves behind, and add no steps to the job.\n- The digest was posted\n")
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:          "45 seconds",
		time.Minute:               "1 minute",
		15 * time.Minute:          "15 minutes",
		time.Hour:                 "1 hour",
		90 * time.Minute:          "1 hour 30 minutes",
		2*time.Hour + time.Minute: "2 hours 1 minute",
	}
	for d, want := range cases {
		require.Equal(t, want, formatDuration(d), d.String())
	}
}

func TestUserMessageStatesTheTimeLeft(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	// The time left is rounded down, so the agent never counts on time it doesn't have
	msg := RunContext{Trigger: "manual", Time: t0, Deadline: t0.Add(14*time.Minute + 59*time.Second)}.UserMessage().Text()
	require.Contains(t, msg, "\nTime left: 14 minutes\n")

	msg = RunContext{Trigger: "manual", Time: t0, Deadline: t0.Add(20 * time.Second)}.UserMessage().Text()
	require.Contains(t, msg, "Time left: less than a minute")

	// A run without a deadline says nothing about time left
	msg = RunContext{Trigger: "manual", Time: t0}.UserMessage().Text()
	require.NotContains(t, msg, "Time left")
}
