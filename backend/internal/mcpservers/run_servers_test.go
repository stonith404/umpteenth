//go:build unit

package mcpservers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// countingServer serves an MCP server with one tool at /mcp and counts the requests it gets
type countingServer struct {
	*httptest.Server
	count atomic.Int64
}

func newCountingServer(t *testing.T, tool string) *countingServer {
	server := sdk.NewServer(&sdk.Implementation{Name: "counting", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: tool}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "done"}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	c := &countingServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.count.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

// attach adds an HTTP server to the workspace and the job
func attach(t *testing.T, m *Module, ctx context.Context, jobID, name, url string) {
	t.Helper()
	server := addServer(t, m, ctx, serverBody{Name: name, Transport: "http", URL: url})
	testutil.Exec(t, m.db, "INSERT INTO job_mcp_servers (job_id, mcp_server_id) VALUES ($1, $2)", jobID, server.ID)
}

func toolNames(tools []agent.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Def().Name)
	}
	return names
}

func (r *runEvents) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.events...)
}

func TestRunServersConnectOnFirstUse(t *testing.T) {
	alpha, beta := newCountingServer(t, "first"), newCountingServer(t, "second")
	m, ctx, wid := newTestModule(t)
	jobID := testutil.SeedJob(t, m.db, wid, "skip")
	attach(t, m, ctx, jobID, "alpha", alpha.URL+"/mcp")
	attach(t, m, ctx, jobID, "beta", beta.URL+"/mcp")

	// Adding a server probes it for an OAuth login, so only the requests from here on count
	alpha.count.Store(0)
	beta.count.Store(0)
	var emitted runEvents
	rc := runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobID}, Emit: emitted.emit}

	// Preparing the run's servers connects none of them
	set, err := m.ToolsForRun(ctx, rc)
	require.NoError(t, err)
	t.Cleanup(set.Close)
	assert.Zero(t, alpha.count.Load())
	assert.Zero(t, beta.count.Load())
	assert.Empty(t, emitted.all())

	// The first use of a server connects that one alone and puts it on the timeline
	tools := set.Connect("alpha")
	assert.Equal(t, []string{mcp.ToolName("alpha", "first")}, toolNames(tools))
	assert.NotZero(t, alpha.count.Load())
	assert.Zero(t, beta.count.Load())
	got := emitted.all()
	require.Len(t, got, 1)
	assert.Equal(t, events.TypeMCPCall, got[0].Type)
	assert.Equal(t, "alpha", got[0].Payload.(map[string]any)["server"])

	// A server connects once, however often the run uses it
	sent := alpha.count.Load()
	assert.Len(t, set.Connect("alpha"), 1)
	assert.Equal(t, sent, alpha.count.Load())
	assert.Len(t, emitted.all(), 1)

	// Connecting every server connects the rest, and the tools keep the order of the job's servers
	tools = set.Connect("")
	assert.Equal(t, []string{mcp.ToolName("alpha", "first"), mcp.ToolName("beta", "second")}, toolNames(tools))
	assert.NotZero(t, beta.count.Load())
	assert.Equal(t, sent, alpha.count.Load())
	assert.Len(t, emitted.all(), 2)

	// A closed set connects nothing anymore
	closed, err := m.ToolsForRun(ctx, rc)
	require.NoError(t, err)
	closed.Close()
	before := beta.count.Load()
	assert.Empty(t, closed.Connect(""))
	assert.Equal(t, before, beta.count.Load())
}

func TestRunServersReportAFailedServerOnce(t *testing.T) {
	m, ctx, wid := newTestModule(t)
	jobID := testutil.SeedJob(t, m.db, wid, "skip")
	attach(t, m, ctx, jobID, "broken", "http://127.0.0.1:1/mcp")

	// A server that can't connect is reported once and not tried again
	var emitted runEvents
	set, err := m.ToolsForRun(ctx, runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobID}, Emit: emitted.emit})
	require.NoError(t, err)
	t.Cleanup(set.Close)
	assert.Empty(t, set.Connect("broken"))
	assert.Empty(t, set.Connect("broken"))
	assert.Empty(t, set.Connect(""))
	reported := emitted.errors()
	require.Len(t, reported, 1)
	assert.Contains(t, reported[0], "MCP server broken is unavailable: failed to connect to broken")

	// The reason stays with the set, so a call of the server's tools can tell why they are missing
	reason, ok := set.Unavailable("broken")
	assert.True(t, ok)
	assert.Contains(t, reason, "failed to connect to broken")
	_, ok = set.Unavailable("elsewhere")
	assert.False(t, ok)

	// A run that stopped before the server connected says nothing about the server, so it reports no error
	runCtx, stop := context.WithCancel(ctx)
	var quiet runEvents
	set, err = m.ToolsForRun(runCtx, runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobID}, Emit: quiet.emit})
	require.NoError(t, err)
	t.Cleanup(set.Close)
	stop()
	assert.Empty(t, set.Connect("broken"))
	assert.Empty(t, quiet.all())
}
