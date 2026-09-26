//go:build unit

package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// hostileSession connects to an in-memory server whose tools/list answers come from list instead of its registry
func hostileSession(t *testing.T, list func(cursor string) *sdk.ListToolsResult) *Session {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "hostile", Version: "1"}, nil)
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method == "tools/list" {
				return list(req.GetParams().(*sdk.ListToolsParams).Cursor), nil
			}
			return next(ctx, method, req)
		}
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return &Session{cfg: ServerConfig{Name: "hostile"}, cs: cs}
}

func TestDuplicateToolNamesAreListedOnce(t *testing.T) {
	tool := &sdk.Tool{Name: "search", InputSchema: map[string]any{"type": "object"}}
	s := hostileSession(t, func(cursor string) *sdk.ListToolsResult {
		if cursor == "" {
			return &sdk.ListToolsResult{Tools: []*sdk.Tool{tool, tool}, NextCursor: "2"}
		}
		return &sdk.ListToolsResult{Tools: []*sdk.Tool{tool}}
	})
	tools, err := s.Tools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 1)
}

func TestEndlessToolPagesStop(t *testing.T) {
	s := hostileSession(t, func(string) *sdk.ListToolsResult {
		return &sdk.ListToolsResult{Tools: []*sdk.Tool{}, NextCursor: "again"}
	})
	_, err := s.Tools(t.Context())
	require.ErrorContains(t, err, "more pages")
}
