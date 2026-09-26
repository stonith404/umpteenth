//go:build unit

package agent_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

func TestToolkitToolPassesArgumentsAsFlags(t *testing.T) {
	adapter := sandboxfake.New()
	adapter.OnFunc(func(cmd []string) bool { return slices.Contains(cmd, "ump-toolkit") }, sandboxfake.Reply{Stdout: `[{"id":1}]`}.Handler())
	sb, err := adapter.Create(context.Background(), sandbox.Spec{Image: "img"})
	require.NoError(t, err)

	tools := &agent.SandboxTools{Sandbox: sb, User: sandbox.UserAgent, DefaultTimeout: time.Minute, MaxTimeout: time.Minute}
	tool := tools.ToolkitTool(agent.ToolkitScript{
		ToolName:    "toolkit__top_stories",
		Path:        "/ump/toolkit/top_stories.py",
		Description: "Print the top stories",
		Args:        map[string]string{"count": "integer", "query": "string?", "tags": "array?"},
	})

	// The schema requires only the arguments without a trailing ?
	def := tool.Def()
	assert.Equal(t, "toolkit__top_stories", def.Name)
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	require.NoError(t, json.Unmarshal(def.Schema, &schema))
	assert.Equal(t, []string{"count"}, schema.Required)
	assert.Equal(t, "integer", schema.Properties["count"]["type"])
	assert.Equal(t, "array", schema.Properties["tags"]["type"])

	// Strings pass through as they are, other values keep their JSON text, and flags come in a stable order
	res := tool.Run(context.Background(), llm.ToolCall{ID: "c1", Name: def.Name, Args: json.RawMessage(`{"query":"a b","count":3,"tags":["x"]}`)})
	require.False(t, res.IsError, res.Content)
	assert.Contains(t, res.Content, `[{"id":1}]`)
	calls := adapter.Calls()
	require.Len(t, calls, 1)
	cmd := calls[0].Cmd
	assert.Equal(t, []string{"ump-toolkit", "/ump/logs/c1.txt", "--count", "3", "--query", "a b", "--tags", `["x"]`}, cmd[3:])
	assert.Contains(t, cmd[2], `'/ump/toolkit/top_stories.py' "${@:2}"`)

	// A missing required argument and an unknown one are errors the agent can fix
	res = tool.Run(context.Background(), llm.ToolCall{ID: "c2", Args: json.RawMessage(`{}`)})
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, `"count"`)
	res = tool.Run(context.Background(), llm.ToolCall{ID: "c3", Args: json.RawMessage(`{"count":1,"limit":2}`)})
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, `"limit"`)
}

// OpenAI rejects every model call of the run when an array argument's schema has no items
func TestToolkitToolArrayArgumentsDescribeTheirItems(t *testing.T) {
	tool := (&agent.SandboxTools{}).ToolkitTool(agent.ToolkitScript{
		ToolName: "toolkit__sync",
		Path:     "/ump/toolkit/sync.sh",
		Args:     map[string]string{"repos": "array", "tags": "array?", "days": "integer"},
	})

	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	require.NoError(t, json.Unmarshal(tool.Def().Schema, &schema))

	// Required and optional arrays accept items of any type, since ump:args doesn't declare one, and other types get no items
	for _, name := range []string{"repos", "tags"} {
		assert.Equal(t, map[string]any{"type": "array", "items": map[string]any{}}, schema.Properties[name], name)
	}
	assert.Equal(t, map[string]any{"type": "integer"}, schema.Properties["days"])
	assert.ElementsMatch(t, []string{"days", "repos"}, schema.Required)
}
