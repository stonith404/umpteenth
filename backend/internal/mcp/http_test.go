//go:build unit

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countingBody struct {
	remaining int64
	closed    bool
}

func (r *countingBody) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.remaining))
	clear(p[:n])
	r.remaining -= int64(n)
	return n, nil
}

func (r *countingBody) Close() error {
	r.closed = true
	return nil
}

func TestHTTPResponseLimitsKeepSSEStreamsOpen(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		limited     bool
	}{
		{"json", http.StatusOK, "application/json", true},
		{"error without content type", http.StatusBadRequest, "", true},
		{"error claiming to be SSE", http.StatusBadRequest, "text/event-stream", true},
		{"SSE stream", http.StatusOK, "text/event-stream; charset=utf-8", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countingBody{remaining: maxHTTPResponseBytes + 1}
			transport := &responseLimitTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.contentType}}, Body: body}, nil
			})}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://mcp.example.com", nil)
			require.NoError(t, err)
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)

			// Finite responses stop at the limit even without Content-Length, but valid event streams can carry multiple bounded events
			n, err := io.Copy(io.Discard, resp.Body)
			if tc.limited {
				var tooLarge *http.MaxBytesError
				require.ErrorAs(t, err, &tooLarge)
				require.EqualValues(t, maxHTTPResponseBytes, n)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, maxHTTPResponseBytes+1, n)
			}
			require.NoError(t, resp.Body.Close())
			require.True(t, body.closed)
		})
	}
}

func TestOversizedHTTPInitializeResponseIsRejected(t *testing.T) {
	// Padding before a valid response makes the client buffer arbitrary data before parsing the result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			return
		}
		if request.Method != "initialize" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		padding := strings.Repeat(" ", 1<<20)
		for range 17 {
			if _, err := io.WriteString(w, padding); err != nil {
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID,
			"result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "hostile", "version": "1"}},
		})
	}))
	t.Cleanup(srv.Close)

	manager := NewManager(egress.New(true))
	session, err := manager.Connect(t.Context(), ServerConfig{Name: "hostile", Transport: TransportHTTP, URL: srv.URL}, nil)
	if session != nil {
		session.Close()
	}
	require.ErrorContains(t, err, "http: request body too large")
}
