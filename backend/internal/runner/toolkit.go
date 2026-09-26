package runner

import (
	"context"
	"log/slog"
	"maps"
	"path"
	"sync"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

// toolkitTools offers each playbook script to the agent as a toolkit__<name> tool (PLAN.md §6.2)
func toolkitTools(ctx context.Context, run Run, job JobConfig, sb *agent.SandboxTools, usage *toolkitCounter) []agent.Tool {
	var tools []agent.Tool
	seen := map[string]bool{}
	for _, s := range job.Toolkit {
		name := playbook.ToolName(s.Name)

		// Sanitizing names can make two scripts collide, and a tool name must stay unique
		if !playbook.ValidScriptName(s.Name) || seen[name] {
			slog.WarnContext(ctx, "Skipping a toolkit script that can't become a tool", slog.String("run", run.ID), slog.String("name", s.Name))
			continue
		}
		seen[name] = true
		tool := sb.ToolkitTool(agent.ToolkitScript{
			ToolName:    name,
			Path:        path.Join("/ump/toolkit", s.Name),
			Description: s.Description,
			Args:        s.Args,
			SideEffects: s.SideEffects,
		})
		tools = append(tools, countedTool{Tool: tool, script: s.Name, usage: usage})
	}
	return tools
}

// toolkitCounter counts toolkit tool calls, which feed the playbook's script stats
type toolkitCounter struct {
	mu    sync.Mutex
	usage map[string]ToolkitUsage
}

func (c *toolkitCounter) add(script string, failed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.usage == nil {
		c.usage = map[string]ToolkitUsage{}
	}
	u := c.usage[script]
	u.Calls++
	if failed {
		u.Failures++
	}
	c.usage[script] = u
}

func (c *toolkitCounter) all() map[string]ToolkitUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]ToolkitUsage, len(c.usage))
	maps.Copy(out, c.usage)
	return out
}

type countedTool struct {
	agent.Tool
	script string
	usage  *toolkitCounter
}

func (t countedTool) Run(ctx context.Context, call llm.ToolCall) agent.Result {
	res := t.Tool.Run(ctx, call)
	t.usage.add(t.script, res.IsError)
	return res
}
