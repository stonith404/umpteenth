//go:build unit

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
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

func TestToolCatalogCountIsBoundedAcrossPages(t *testing.T) {
	s := hostileSession(t, func(cursor string) *sdk.ListToolsResult {
		page := 0
		if cursor != "" {
			_, _ = fmt.Sscanf(cursor, "%d", &page)
		}
		tools := make([]*sdk.Tool, 0, 100)
		for i := range 100 {
			tools = append(tools, &sdk.Tool{Name: fmt.Sprintf("tool-%d", page*100+i), InputSchema: map[string]any{"type": "object"}})
		}
		next := ""
		if page < 5 {
			next = fmt.Sprint(page + 1)
		}
		return &sdk.ListToolsResult{Tools: tools, NextCursor: next}
	})

	_, err := s.Tools(t.Context())
	require.ErrorContains(t, err, "at most 512 MCP tools")
}

func TestToolCatalogBytesAreBounded(t *testing.T) {
	s := hostileSession(t, func(string) *sdk.ListToolsResult {
		return &sdk.ListToolsResult{Tools: []*sdk.Tool{{Name: "oversized", Description: strings.Repeat("x", maxToolBytes), InputSchema: map[string]any{"type": "object"}}}}
	})

	_, err := s.Tools(t.Context())
	require.ErrorContains(t, err, "may total at most")
}

func TestToolNamesCannotCollideAfterSanitizingOrTruncating(t *testing.T) {
	pairs := [][2]string{
		{"a", "b/c"},
		{"a", "b?c"},
		{"a", "b__c"},
		{"a__b", "c"},
		{strings.Repeat("a", 64), "first"},
		{strings.Repeat("a", 64), "second"},
	}
	seen := map[string]bool{}
	for _, pair := range pairs {
		name := ToolName(pair[0], pair[1])
		require.LessOrEqual(t, len(name), 64)
		require.Regexp(t, `^[A-Za-z0-9_-]+$`, name)
		require.False(t, seen[name], "duplicate provider name %q", name)
		seen[name] = true
		require.Equal(t, name, ToolName(pair[0], pair[1]))
	}
}

func TestHeadersStayWithTheServerOrigin(t *testing.T) {
	var mu sync.Mutex
	var elsewhereKeys []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhereKeys = append(elsewhereKeys, r.Header.Get("X-Api-Key"))
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(elsewhere.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)

	// The redirect is followed, but the configured credentials stay behind
	m := NewManager(egress.New(true))
	_, err := m.Connect(t.Context(), ServerConfig{Name: "r", Transport: TransportHTTP, URL: srv.URL + "/mcp", Headers: map[string]string{"X-Api-Key": "key"}}, nil)
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, elsewhereKeys)
	for _, key := range elsewhereKeys {
		require.Empty(t, key)
	}
}
