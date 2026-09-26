package agent

import (
	"context"
	"encoding/json"
	"maps"
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
		prop := map[string]any{"type": base}
		if base == "array" {
			// OpenAI rejects the whole request when an array has no items, and ump:args leaves the item type open
			prop["items"] = map[string]any{}
		}
		props[name] = prop
		if base == typ {
			required = append(required, name)
		}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return raw
}

func (t *toolkitTool) argNames() []string {
	return slices.Sorted(maps.Keys(t.script.Args))
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

	timeout := t.s.DefaultTimeout
	if t.script.Timeout > 0 {
		timeout = t.script.Timeout
	}
	return t.s.exec(ctx, call.ID, "ump-toolkit", shellQuote(t.script.Path)+` "${@:2}"`, argv, timeout)
}

// shellQuote quotes s as one word for bash, where nothing inside single quotes is expanded
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
