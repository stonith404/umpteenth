//go:build unit

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
)

// legacySSEServer serves only the 2024-11-05 HTTP+SSE transport, as older servers and SSE-mode proxies do
func legacySSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "legacy", Version: "1"}, nil)
	type echoInput struct {
		Text string `json:"text"`
	}
	sdk.AddTool(server, &sdk.Tool{Name: "echo"}, func(_ context.Context, _ *sdk.CallToolRequest, in echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo:" + in.Text}}}, nil, nil
	})
	handler := sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)

	// A Streamable HTTP POST to the SSE endpoint is rejected, which makes the client fall back to the legacy transport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("sessionid") == "" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLegacySSESessionOutlivesTheConnectContext(t *testing.T) {
	srv := legacySSEServer(t)
	m := NewManager(egress.New(true))

	// Connect and list tools under a bounded context that ends right after, as a run does before the agent starts
	connectCtx, cancel := context.WithTimeout(t.Context(), time.Minute)
	session, err := m.Connect(connectCtx, ServerConfig{Name: "legacy", Transport: TransportHTTP, URL: srv.URL + "/sse"}, nil)
	require.NoError(t, err)
	t.Cleanup(session.Close)
	tools, err := session.Tools(connectCtx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	cancel()

	// The agent calls the tool later, under the run's own context
	callCtx, callCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer callCancel()
	out, isError, err := session.Call(callCtx, "echo", json.RawMessage(`{"text":"hi"}`))
	require.NoError(t, err)
	require.False(t, isError)
	require.Equal(t, "echo:hi", out)
}
