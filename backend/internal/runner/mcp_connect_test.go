//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

// fakeMCP is a job's notes MCP server with one tool, which counts how often runs connected it
type fakeMCP struct {
	mu       sync.Mutex
	connects int
}

func (f *fakeMCP) ToolsForRun(_ context.Context, rc runner.RunContext) (runner.RunTools, error) {
	return &fakeMCPRun{server: f, rc: rc}, nil
}

func (f *fakeMCP) connected() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects
}

// fakeMCPRun is the notes server of one run, which connects on its first use like a real one
type fakeMCPRun struct {
	server *fakeMCP
	rc     runner.RunContext
	mu     sync.Mutex
	tools  []agent.Tool
}

func (r *fakeMCPRun) Connect(server string) []agent.Tool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil && (server == "" || server == "notes") {
		r.server.mu.Lock()
		r.server.connects++
		r.server.mu.Unlock()
		r.rc.Emit(events.Event{Type: events.TypeMCPCall, Payload: map[string]any{"server": "notes", "tools": 1}})
		r.tools = []agent.Tool{listNotes{}}
	}
	return slices.Clone(r.tools)
}

func (r *fakeMCPRun) Unavailable(string) (string, bool) { return "", false }
func (r *fakeMCPRun) Close()                            {}

// listNotes is the notes server's one tool
type listNotes struct{}

func (listNotes) Def() llm.ToolDef {
	return llm.ToolDef{Name: mcp.ToolName("notes", "list_notes"), Schema: json.RawMessage(`{"type":"object","properties":{}}`)}
}
func (listNotes) ReadOnly() bool { return true }
func (listNotes) Run(context.Context, llm.ToolCall) agent.Result {
	return agent.Result{Content: "[]"}
}

// steps returns the run's timeline as event types, with each status change named by its status, such as run.status:running
func (h *harness) steps(t *testing.T, runID string) []string {
	t.Helper()
	rows, err := h.db.QueryContext(context.Background(), "SELECT type, payload FROM run_events WHERE run_id = $1 ORDER BY seq", runID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, raw string
		require.NoError(t, rows.Scan(&typ, &raw))
		if typ == events.TypeRunStatus {
			var payload struct {
				Status string `json:"status"`
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &payload))
			typ += ":" + payload.Status
		}
		out = append(out, typ)
	}
	require.NoError(t, rows.Err())
	return out
}

// toolNamesOf returns the names of the tools a model request offered
func toolNamesOf(req llm.Request) []string {
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestAgentRunConnectsMCPServersBeforeItsFirstTurn(t *testing.T) {
	notes := &fakeMCP{}
	h := newHarnessWith(t, baseImages{}, nil, notes)
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}})
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.Equal(t, 1, notes.connected())

	// The server is on the timeline before the run starts, and the agent's first turn already offers its tool
	steps := h.steps(t, id)
	assert.Less(t, slices.Index(steps, events.TypeMCPCall), slices.Index(steps, events.TypeRunStatus+":"+runner.StatusRunning))
	assert.Less(t, slices.Index(steps, events.TypeRunStatus+":"+runner.StatusRunning), slices.Index(steps, events.TypeLLMCall))
	requests := h.provider.Requests()
	require.NotEmpty(t, requests)
	assert.Contains(t, toolNamesOf(requests[0]), mcp.ToolName("notes", "list_notes"))
}

func TestScriptedRunConnectsMCPServersOnlyWhenMainCallsThem(t *testing.T) {
	notes := &fakeMCP{}
	h := newHarnessWith(t, baseImages{}, nil, notes)

	// A main script that never calls ump mcp never waits for a server
	id := h.graduate(t, `{"checks":[],"llm":false}`)
	h.onMain(id, "done\n", 0, nil)
	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.Zero(t, notes.connected())
	assert.NotContains(t, h.eventTypes(t, id), events.TypeMCPCall)

	// A main script that calls a server connects it right then, the way the broker does for ump mcp call
	notes = &fakeMCP{}
	h = newHarnessWith(t, baseImages{}, nil, notes)
	id = h.graduate(t, `{"checks":[],"llm":false}`)
	h.onMain(id, "done\n", 0, func(live *runner.LiveRun) {
		live.ConnectTools("notes")
		_, ok := live.Tool(mcp.ToolName("notes", "list_notes"))
		assert.True(t, ok, "the server's tool is callable once it connected")
	})
	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ = h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.Equal(t, 1, notes.connected())
	steps := h.steps(t, id)
	assert.Equal(t, 1, countOf(steps, events.TypeMCPCall))
	assert.Less(t, slices.Index(steps, events.TypeMCPCall), slices.Index(steps, events.TypeVerifyResult))
	assert.Empty(t, h.provider.Requests())
}

func TestScriptedRunConnectsMCPServersBeforeTheAgentTakesOver(t *testing.T) {
	notes := &fakeMCP{}
	h := newHarnessWith(t, baseImages{}, nil, notes)
	id := h.graduate(t, `{"checks":[],"llm":false}`)
	h.onMain(id, "Traceback\n", 1, nil)
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "Done by the agent"}}}})

	require.NoError(t, h.runner.Execute(context.Background(), id))

	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.True(t, h.fellBack(t, id))
	assert.Equal(t, 1, notes.connected())

	// The servers main never called connect once the agent takes over, before its first turn
	steps := h.steps(t, id)
	assert.Less(t, slices.Index(steps, events.TypeFallback), slices.Index(steps, events.TypeMCPCall))
	assert.Less(t, slices.Index(steps, events.TypeMCPCall), slices.Index(steps, events.TypeLLMCall))
	requests := h.provider.Requests()
	require.Len(t, requests, 1)
	assert.Contains(t, toolNamesOf(requests[0]), mcp.ToolName("notes", "list_notes"))
}

func countOf(items []string, item string) int {
	n := 0
	for _, it := range items {
		if it == item {
			n++
		}
	}
	return n
}
