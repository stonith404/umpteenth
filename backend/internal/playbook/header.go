package playbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ScriptHeader is the ump: header at the top of a candidate or toolkit script (PLAN.md §10.2)
type ScriptHeader struct {
	Name        string
	Description string
	// Args maps each argument name to its type, which the toolkit tool turns into a JSON schema
	Args        map[string]string
	SideEffects bool
	Lang        string
	// Found reports whether the script has a ump: header at all
	Found bool
}

// argTypes are the argument types a script header may declare
var argTypes = []string{"string", "integer", "number", "boolean", "object", "array"}

var (
	// Shell and Python scripts comment with #, Node scripts with //
	headerLine = regexp.MustCompile(`^\s*(?:#|//)\s*ump:([a-z-]+)\s*(.*)$`)
	argName    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// maxHeaderLines bounds how far into a script the header is looked for, so a ump: line deep inside the code is not mistaken for it
const maxHeaderLines = 30

// ParseScriptHeader reads the ump: header of a script
// A script without a header has no name, and the caller decides whether that is acceptable
func ParseScriptHeader(content string) (ScriptHeader, error) {
	h := ScriptHeader{Lang: langFromShebang(content)}
	lines := strings.SplitN(content, "\n", maxHeaderLines+1)
	for _, line := range lines[:min(len(lines), maxHeaderLines)] {
		m := headerLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		h.Found = true
		switch key {
		case "name":
			h.Name = value
		case "description":
			h.Description = value
		case "args":
			if value == "" {
				continue
			}
			args, err := parseArgs(value)
			if err != nil {
				return h, err
			}
			h.Args = args
		case "side-effects":
			// The example header carries a trailing comment listing the choices, which is not part of the value
			value, _, _ = strings.Cut(value, "#")
			switch strings.TrimSpace(value) {
			case "", "none":
				h.SideEffects = false
			case "external":
				h.SideEffects = true
			default:
				return h, fmt.Errorf("ump:side-effects must be none or external, not %q", strings.TrimSpace(value))
			}
		}
	}
	return h, nil
}

func parseArgs(value string) (map[string]string, error) {
	var args map[string]string
	err := json.Unmarshal([]byte(value), &args)
	if err != nil {
		return nil, errors.New(`ump:args must be a JSON object of argument names and types, e.g. {"repo":"string","days":"integer"}`)
	}
	for name, typ := range args {
		if !argName.MatchString(name) {
			return nil, fmt.Errorf("ump:args name %q must be lowercase snake_case", name)
		}
		if !slices.Contains(argTypes, strings.TrimSuffix(typ, "?")) {
			return nil, fmt.Errorf("ump:args type %q of %s must be one of %s, with a trailing ? for optional arguments", typ, name, strings.Join(argTypes, ", "))
		}
	}
	return args, nil
}

// argsJSON is how a script's arguments are stored, or nil for a script without any
func (h ScriptHeader) argsJSON() json.RawMessage {
	if len(h.Args) == 0 {
		return nil
	}
	raw, _ := json.Marshal(h.Args)
	return raw
}

// langFromShebang names the interpreter a script runs with, defaulting to bash for scripts without a shebang
func langFromShebang(content string) string {
	first, _, _ := strings.Cut(content, "\n")
	if !strings.HasPrefix(first, "#!") {
		return "bash"
	}
	switch {
	case strings.Contains(first, "python"):
		return "python"
	case strings.Contains(first, "node"):
		return "node"
	case strings.Contains(first, "bash"):
		return "bash"
	case strings.HasSuffix(strings.TrimSpace(first), "sh"):
		return "sh"
	}
	return "bash"
}

// shebangFor returns the interpreter line a script of a language needs to be executable
func shebangFor(lang string) string {
	switch lang {
	case "python":
		return "#!/usr/bin/env python3"
	case "node":
		return "#!/usr/bin/env node"
	case "sh":
		return "#!/bin/sh"
	}
	return "#!/usr/bin/env bash"
}

// executable gives a script without an interpreter line the one its language needs, since toolkit scripts and main are run directly
func executable(script, lang string) string {
	if !strings.HasPrefix(script, "#!") {
		script = shebangFor(lang) + "\n" + script
	}
	return script + "\n"
}
