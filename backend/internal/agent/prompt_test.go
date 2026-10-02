//go:build unit

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

func TestSystemBlocksWithoutSkillsAreUnchanged(t *testing.T) {
	// A job without skills keeps the blocks it had before skills existed, so its prompt cache keeps hitting
	blocks := SystemBlocks(PromptInput{Instruction: "Do it"})
	require.Equal(t, []llm.Block{{Text: baseInstructions, CacheBreakpoint: true}, {Text: "## The job\nDo it\n", CacheBreakpoint: true}}, blocks)

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
