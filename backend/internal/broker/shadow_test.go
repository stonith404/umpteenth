//go:build unit

package broker

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// mcpTool stands in for an MCP tool and counts its calls
type mcpTool struct {
	name     string
	readOnly bool
	calls    int
}

func (t *mcpTool) Def() llm.ToolDef { return llm.ToolDef{Name: t.name} }
func (t *mcpTool) ReadOnly() bool   { return t.readOnly }
func (t *mcpTool) Run(context.Context, llm.ToolCall) agent.Result {
	t.calls++
	return agent.Result{Content: "done"}
}

// memoryState is a job's state held in memory
type memoryState map[string]string

func (s memoryState) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := s[key]
	return v, ok, nil
}

func (s memoryState) Set(_ context.Context, key, value string) error {
	s[key] = value
	return nil
}

func (s memoryState) List(context.Context) (map[string]string, error) {
	return maps.Clone(s), nil
}

type memoryStates struct{ state memoryState }

func (s memoryStates) ForJob(string) runner.JobState { return s.state }

func TestShadowRunsReachTheBrokerWithoutChangingAnything(t *testing.T) {
	jobState := memoryState{"cursor": "1"}
	search := &mcpTool{name: mcp.ToolName("github", "search"), readOnly: true}
	post := &mcpTool{name: mcp.ToolName("slack", "post_message")}

	// The shadow run has no run row, so only the registry knows its token
	live := runner.NewRegistry()
	shadow := &runner.LiveRun{Run: runner.Run{ID: "shadow"}, Shadow: true, State: overlay{base: jobState, writes: map[string]string{}}}
	shadow.SetTools([]agent.Tool{search, post})
	live.RegisterShadow(crypto.HashToken("shadow-token"), shadow)
	b := New(Dependencies{Runs: tokenRuns{}, Live: live, State: memoryStates{jobState}})
	server := httptest.NewServer(b.Handler())
	t.Cleanup(server.Close)

	call := func(method, path, body string) (int, string) {
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer shadow-token")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var out json.RawMessage
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, string(out)
	}

	// Read-only MCP tools work, and ones that may change something are refused and noted
	status, _ := call(http.MethodPost, "/v1/mcp/call", `{"server":"github","tool":"search","arguments":{}}`)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, 1, search.calls)
	status, _ = call(http.MethodPost, "/v1/mcp/call", `{"server":"slack","tool":"post_message","arguments":{"text":"hi"}}`)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Zero(t, post.calls)
	assert.Equal(t, []string{`MCP tool slack.post_message with {"text":"hi"}`}, shadow.Refused())
	assert.Empty(t, shadow.Actions())

	// State reads the job's own and keeps writes to the shadow run
	status, body := call(http.MethodPut, "/v1/state/cursor", `{"value":"2"}`)
	require.Equal(t, http.StatusOK, status, body)
	_, body = call(http.MethodGet, "/v1/state/cursor", "")
	assert.JSONEq(t, `{"key":"cursor","value":"2","found":true}`, body)
	assert.Equal(t, "1", jobState["cursor"])

	// Outputs land on the shadow run like on any live run
	status, _ = call(http.MethodPost, "/v1/output", `{"key":"count","value":2}`)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `2`, string(shadow.Outputs()["count"]))

	// Once the shadow run is over its token stops working
	live.UnregisterShadow(crypto.HashToken("shadow-token"))
	status, _ = call(http.MethodGet, "/v1/state/cursor", "")
	assert.Equal(t, http.StatusUnauthorized, status)
}

// overlay keeps writes to itself like a shadow run's state, which the runner package doesn't export
type overlay struct {
	base   runner.JobState
	writes map[string]string
}

func (o overlay) Get(ctx context.Context, key string) (string, bool, error) {
	if v, ok := o.writes[key]; ok {
		return v, true, nil
	}
	return o.base.Get(ctx, key)
}

func (o overlay) Set(_ context.Context, key, value string) error {
	o.writes[key] = value
	return nil
}

func (o overlay) List(ctx context.Context) (map[string]string, error) {
	out, err := o.base.List(ctx)
	if err != nil {
		return nil, err
	}
	maps.Copy(out, o.writes)
	return out, nil
}
