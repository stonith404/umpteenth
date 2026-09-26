//go:build unit

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

// runEditFile runs edit_file on a file that holds content and returns the result and what the tool wrote back, if it wrote at all
func runEditFile(t *testing.T, content string, args map[string]any) (Result, []byte, bool) {
	t.Helper()
	adapter := sandboxfake.New()
	adapter.OnFunc(func(cmd []string) bool { return slices.Contains(cmd, "ump-read") }, sandboxfake.Reply{Stdout: content}.Handler())
	adapter.OnFunc(func(cmd []string) bool { return slices.Contains(cmd, "ump-write") }, sandboxfake.Reply{}.Handler())
	sb, err := adapter.Create(context.Background(), sandbox.Spec{Image: "img"})
	require.NoError(t, err)

	raw, err := json.Marshal(args)
	require.NoError(t, err)
	tools := &SandboxTools{Sandbox: sb, User: sandbox.UserAgent, DefaultTimeout: time.Minute, MaxTimeout: time.Minute}
	res := (&editFileTool{tools}).Run(context.Background(), llm.ToolCall{ID: "c1", Name: "edit_file", Args: raw})

	for _, c := range adapter.Calls() {
		if slices.Contains(c.Cmd, "ump-write") {
			return res, c.Stdin, true
		}
	}
	return res, nil, false
}

func TestEditFileRejectsResultsLargerThanTheReadLimit(t *testing.T) {
	half := strings.Repeat("a", maxReadBytes/2)
	full := strings.Repeat("a", maxReadBytes)

	// A result of exactly the read limit is still written
	res, written, ok := runEditFile(t, half, map[string]any{"path": "f.txt", "old": "a", "new": "bb", "replace_all": true})
	require.False(t, res.IsError, res.Content)
	require.True(t, ok)
	assert.Len(t, written, maxReadBytes)

	// One byte more is refused before anything is written
	res, _, ok = runEditFile(t, half+"c", map[string]any{"path": "f.txt", "old": "a", "new": "bb", "replace_all": true})
	require.True(t, res.IsError)
	assert.Contains(t, res.Content, "larger than")
	assert.False(t, ok)

	// A single replacement is bounded the same way
	res, _, ok = runEditFile(t, full[1:]+"x", map[string]any{"path": "f.txt", "old": "x", "new": "yy"})
	require.True(t, res.IsError)
	assert.Contains(t, res.Content, "larger than")
	assert.False(t, ok)

	// A short old that occurs everywhere with a longer new would multiply the file, which is the case the bound exists for
	res, _, ok = runEditFile(t, full, map[string]any{"path": "f.txt", "old": "a", "new": strings.Repeat("B", 64), "replace_all": true})
	require.True(t, res.IsError)
	assert.Contains(t, res.Content, "larger than")
	assert.False(t, ok)

	// Edits that don't grow the file still work on a file at the read limit
	res, written, ok = runEditFile(t, full, map[string]any{"path": "f.txt", "old": "aa", "new": "b", "replace_all": true})
	require.False(t, res.IsError, res.Content)
	require.True(t, ok)
	assert.Len(t, written, maxReadBytes/2)
}
