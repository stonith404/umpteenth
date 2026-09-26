package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// ToolkitScript describes a playbook script the agent can call as a tool (PLAN.md §6.2)
type ToolkitScript struct {
	// ToolName is the tool's name, toolkit__ followed by the script name
	ToolName    string
	Path        string
	Description string
	// Args maps argument names to their ump:args types, where a trailing ? marks an optional argument
	Args        map[string]string
	SideEffects bool
	// Timeout overrides the default command timeout, e.g. for a main script that does the whole job
	Timeout time.Duration
}

// ToolkitTool runs a toolkit script with its arguments as --name value flags
func (s *SandboxTools) ToolkitTool(script ToolkitScript) Tool {
	return &toolkitTool{s: s, script: script}
}

type toolkitTool struct {
	s      *SandboxTools
	script ToolkitScript
}

// Toolkit scripts may write files, so they never run in parallel with other tools
func (t *toolkitTool) ReadOnly() bool { return false }

func (t *toolkitTool) Def() llm.ToolDef {
	desc := t.script.Description + "\n\nRuns " + t.script.Path + ", a script proven in earlier runs of this job."
	if t.script.SideEffects {
		desc += " It has external side effects, so call it only when the job needs them."
	}
	return llm.ToolDef{Name: t.script.ToolName, Description: desc, Schema: t.schema()}
}

// schema turns the ump:args types into a JSON schema
func (t *toolkitTool) schema() json.RawMessage {
	props := map[string]any{}
	required := []string{}
	for _, name := range t.argNames() {
		typ := t.script.Args[name]
		base := strings.TrimSuffix(typ, "?")
		props[name] = map[string]any{"type": base}
		if base == typ {
			required = append(required, name)
		}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return raw
}

func (t *toolkitTool) argNames() []string {
	names := make([]string, 0, len(t.script.Args))
	for name := range t.script.Args {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (t *toolkitTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args map[string]json.RawMessage
	if len(call.Args) > 0 && string(call.Args) != "null" {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return Errorf("%s takes a JSON object of arguments.", t.script.ToolName)
		}
	}

	// Arguments become --name value flags in a stable order, and strings pass through untouched while other JSON values keep their JSON text
	var argv []string
	for _, name := range t.argNames() {
		raw, ok := args[name]
		if !ok || string(raw) == "null" {
			if !strings.HasSuffix(t.script.Args[name], "?") {
				return Errorf("%s needs the argument %q.", t.script.ToolName, name)
			}
			continue
		}
		var str string
		value := string(raw)
		if json.Unmarshal(raw, &str) == nil {
			value = str
		}
		argv = append(argv, "--"+name, value)
	}
	for name := range args {
		if _, known := t.script.Args[name]; !known {
			return Errorf("%s has no argument %q.", t.script.ToolName, name)
		}
	}

	script := fmt.Sprintf(`mkdir -p /ump/logs; %q "${@:2}" < /dev/null 2>&1 | tee "$1"; exit "${PIPESTATUS[0]}"`, t.script.Path)
	timeout := t.s.DefaultTimeout
	if t.script.Timeout > 0 {
		timeout = t.script.Timeout
	}
	return t.s.exec(ctx, call.ID, "ump-toolkit", script, argv, timeout)
}
