// Package playbook stores what a job has learned as immutable versions (PLAN.md §9)
package playbook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// Learning is one piece of know-how
type Learning struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	When    string   `json:"when,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Hits    int      `json:"hits"`
	Status  string   `json:"status" enum:"active,retired"`
}

// ScriptStats counts how a toolkit script performs
type ScriptStats struct {
	Calls    int `json:"calls"`
	Failures int `json:"failures"`
}

// Script is one toolkit script, injected into /ump/toolkit
type Script struct {
	Name        string          `json:"name"`
	Lang        string          `json:"lang"`
	Description string          `json:"description"`
	Args        json.RawMessage `json:"args,omitempty"`
	SideEffects bool            `json:"sideEffects"`
	Content     string          `json:"content"`
	Sources     []string        `json:"sources,omitempty"`
	Stats       ScriptStats     `json:"stats"`
}

// Content is one playbook version
type Content struct {
	Learnings  []Learning      `json:"learnings"`
	Toolkit    []Script        `json:"toolkit"`
	Dockerfile *string         `json:"dockerfile"`
	Setup      *string         `json:"setup"`
	Main       *string         `json:"main"`
	Verify     json.RawMessage `json:"verify,omitempty"`
}

// Normalize replaces nil lists so the JSON shape is stable
func (c *Content) Normalize() {
	if c.Learnings == nil {
		c.Learnings = []Learning{}
	}
	if c.Toolkit == nil {
		c.Toolkit = []Script{}
	}
	if c.Dockerfile != nil && strings.TrimSpace(*c.Dockerfile) == "" {
		c.Dockerfile = nil
	}
	if c.Setup != nil && strings.TrimSpace(*c.Setup) == "" {
		c.Setup = nil
	}
	if c.Main != nil && strings.TrimSpace(*c.Main) == "" {
		c.Main = nil
	}
	if string(c.Verify) == "null" {
		c.Verify = nil
	}
}

// IsEmpty reports whether the playbook holds no know-how yet, which makes a run an Explore run
func (c Content) IsEmpty() bool {
	for _, l := range c.Learnings {
		if l.Status != "retired" {
			return false
		}
	}
	return len(c.Toolkit) == 0 && c.Main == nil
}

// scriptName is the shape of a toolkit script name, which becomes a file name in /ump/toolkit and must never contain a path separator
var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ValidScriptName reports whether a toolkit script name is safe to use as a file name in /ump/toolkit
func ValidScriptName(name string) bool {
	return scriptName.MatchString(name) && !strings.Contains(name, "..")
}

// Validate rejects content that would be unsafe to inject into a sandbox, that a scripted run couldn't check, or whose learnings can't be told apart
func (c Content) Validate() error {
	if _, err := ParseVerify(c.Verify); err != nil {
		return apperror.InvalidField("content.verify", "invalid", err.Error())
	}
	seen := make(map[string]bool, len(c.Toolkit))
	for i, s := range c.Toolkit {
		field := fmt.Sprintf("content.toolkit[%d].name", i)
		if !ValidScriptName(s.Name) {
			return apperror.InvalidField(field, "invalid", "must start with a letter or digit and may only contain up to 64 letters, digits, dots, dashes and underscores")
		}

		// Two scripts with one tool name would overwrite each other in /ump/toolkit or leave one of them out of the agent's tools
		if seen[ToolName(s.Name)] {
			return apperror.InvalidField(field, "duplicate", "is used by another toolkit script, or becomes the same tool name as one")
		}
		seen[ToolName(s.Name)] = true
	}

	// Learnings are told apart by their id, on the playbook page and in reflection's operations, so two learnings must never share one
	ids := make(map[string]bool, len(c.Learnings))
	for i, l := range c.Learnings {
		if ids[l.ID] {
			return apperror.InvalidField(fmt.Sprintf("content.learnings[%d].id", i), "duplicate", "is already used by another learning")
		}
		ids[l.ID] = true
	}
	return nil
}

// syncScriptHeaders takes each toolkit script's description, arguments and side effects from its ump: header
// A script edited by hand then keeps its tool definition in step with its code, while a script without a header keeps what was stored
func (c *Content) syncScriptHeaders() error {
	for i := range c.Toolkit {
		s := &c.Toolkit[i]
		h, err := ParseScriptHeader(s.Content)
		field := fmt.Sprintf("content.toolkit[%d].content", i)
		if err != nil {
			return apperror.InvalidField(field, "invalid", err.Error())
		}
		if !h.Found {
			continue
		}
		if h.Name != "" && h.Name != s.Name {
			return apperror.InvalidField(field, "invalid", fmt.Sprintf("the ump:name header says %q but the script is named %q", h.Name, s.Name))
		}
		if h.Description != "" {
			s.Description = h.Description
		}
		s.Args = h.argsJSON()
		s.SideEffects = h.SideEffects
		if strings.HasPrefix(s.Content, "#!") {
			s.Lang = h.Lang
		}
	}
	return nil
}

// DockerfileHash identifies a Dockerfile's content, or returns an empty string when there is none
func (c Content) DockerfileHash() string {
	if c.Dockerfile == nil {
		return ""
	}
	return HashDockerfile(*c.Dockerfile)
}

// HashDockerfile hashes Dockerfile text, ignoring trailing whitespace differences
func HashDockerfile(dockerfile string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(dockerfile)))
	return hex.EncodeToString(sum[:])
}

// Render formats the playbook for the prompt; it is deterministic so the prompt prefix stays cacheable
func (c Content) Render() string {
	var b strings.Builder
	active := 0
	for _, l := range c.Learnings {
		if l.Status == "retired" {
			continue
		}
		if active == 0 {
			b.WriteString("### Learnings\n")
		}
		active++
		if l.When != "" {
			fmt.Fprintf(&b, "- [%s] %s (when %s)\n", l.ID, l.Text, l.When)
		} else {
			fmt.Fprintf(&b, "- [%s] %s\n", l.ID, l.Text)
		}
	}
	if len(c.Toolkit) > 0 {
		if active > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### Toolkit\nScripts proven in earlier runs. Call them as tools; each is also in /ump/toolkit for your own scripts, taking its arguments as --name value flags:\n")
		for _, s := range c.Toolkit {
			fmt.Fprintf(&b, "- %s: %s", ToolName(s.Name), s.Description)
			if s.SideEffects {
				b.WriteString(" (has external side effects)")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// toolNameChars are the characters every provider accepts in a tool name
var toolNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ToolName is the name of the agent tool that runs a toolkit script
func ToolName(script string) string {
	name := "toolkit__" + toolNameChars.ReplaceAllString(script, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// Markdown renders the human-readable PLAYBOOK.md injected into the sandbox
func (c Content) Markdown() string {
	rendered := c.Render()
	if rendered == "" {
		return "# Playbook\n\nThis job has not learned anything yet.\n"
	}
	return "# Playbook\n\n" + rendered
}
