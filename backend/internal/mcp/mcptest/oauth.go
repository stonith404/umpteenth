//go:build unit

// Package mcptest holds an OAuth-protected MCP server and its authorization server for tests
package mcptest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ClientName is the client name the app registers with, which the fake insists on
const ClientName = "Umpteenth"

// AuthServer is an OAuth authorization server with dynamic client registration, PKCE and rotating refresh tokens
// It approves every authorization request, as a user clicking allow would
type AuthServer struct {
	*httptest.Server

	mu sync.Mutex
	// AccessTTL is the lifetime handed out with access tokens, 0 leaves expires_in out
	AccessTTL int
	// WithoutRegistration hides the registration endpoint
	WithoutRegistration bool
	// RejectScopes answers authorization requests that ask for scopes with invalid_scope
	RejectScopes bool
	clients      map[string]string
	codes        map[string]url.Values
	access       map[string]bool
	refresh      map[string]bool
	refreshes    int
	issued       int
	authorize    url.Values
	apiKeys      []string
}

func NewAuthServer(t *testing.T) *AuthServer {
	a := &AuthServer{AccessTTL: 3600, clients: map[string]string{}, codes: map[string]url.Values{}, access: map[string]bool{}, refresh: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", a.metadata)
	mux.HandleFunc("POST /register", a.register)
	mux.HandleFunc("GET /authorize", a.authorizeRequest)
	mux.HandleFunc("POST /token", a.token)
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.apiKeys = append(a.apiKeys, r.Header.Get("X-Api-Key"))
		a.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *AuthServer) metadata(w http.ResponseWriter, _ *http.Request) {
	meta := map[string]any{
		"issuer": a.URL, "authorization_endpoint": a.URL + "/authorize", "token_endpoint": a.URL + "/token",
		"scopes_supported": []string{"read", "write", "offline_access"}, "code_challenge_methods_supported": []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "authorization_response_iss_parameter_supported": true,
	}
	if !a.WithoutRegistration {
		meta["registration_endpoint"] = a.URL + "/register"
	}
	WriteJSON(w, http.StatusOK, meta)
}

func (a *AuthServer) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) != 1 || req.AuthMethod != "none" || req.ClientName != ClientName {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	a.mu.Lock()
	id := fmt.Sprintf("client-%d", len(a.clients)+1)
	a.clients[id] = req.RedirectURIs[0]
	a.mu.Unlock()
	WriteJSON(w, http.StatusCreated, map[string]any{"client_id": id, "redirect_uris": req.RedirectURIs, "token_endpoint_auth_method": "none"})
}

func (a *AuthServer) authorizeRequest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorize = q
	if a.clients[q.Get("client_id")] != q.Get("redirect_uri") && !strings.HasPrefix(q.Get("client_id"), "preregistered") {
		http.Error(w, "unknown client or redirect", http.StatusBadRequest)
		return
	}
	back, _ := url.Parse(q.Get("redirect_uri"))
	if a.RejectScopes && q.Get("scope") != "" {
		back.RawQuery = url.Values{"error": {"invalid_scope"}, "state": {q.Get("state")}, "iss": {a.URL}}.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound) // #nosec G710 -- like a real authorization server, the fake redirects to the client's registered redirect URI
		return
	}
	code := fmt.Sprintf("code-%d", len(a.codes)+1)
	a.codes[code] = q
	back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {a.URL}}.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound) // #nosec G710 -- like a real authorization server, the fake redirects to the client's registered redirect URI
}

func (a *AuthServer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		// The code must come with the PKCE verifier, redirect URI, resource and client it was issued for
		auth, ok := a.codes[r.Form.Get("code")]
		delete(a.codes, r.Form.Get("code"))
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != auth.Get("code_challenge") || r.Form.Get("redirect_uri") != auth.Get("redirect_uri") ||
			r.Form.Get("resource") != auth.Get("resource") || r.Form.Get("client_id") != auth.Get("client_id") {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
	case "refresh_token":
		// Refresh tokens rotate, so each one works once
		if !a.refresh[r.Form.Get("refresh_token")] {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "refresh token revoked"})
			return
		}
		delete(a.refresh, r.Form.Get("refresh_token"))
		a.refreshes++
	default:
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	a.issued++
	access, refresh := fmt.Sprintf("access-%d", a.issued), fmt.Sprintf("refresh-%d", a.issued)
	a.access[access] = true
	a.refresh[refresh] = true
	body := map[string]any{"access_token": access, "token_type": "Bearer", "refresh_token": refresh}
	if a.AccessTTL > 0 {
		body["expires_in"] = a.AccessTTL
	}
	WriteJSON(w, http.StatusOK, body)
}

// Revoke makes every access token invalid, as a server that rotated its keys would
func (a *AuthServer) Revoke() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.access = map[string]bool{}
}

// Valid reports whether an access token is currently accepted
func (a *AuthServer) Valid(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.access[token]
}

// Refreshes counts the refresh grants
func (a *AuthServer) Refreshes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshes
}

// Clients counts the dynamically registered clients
func (a *AuthServer) Clients() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.clients)
}

// LastAuthorize returns the query of the last authorization request
func (a *AuthServer) LastAuthorize() url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authorize
}

// APIKeys returns the X-Api-Key header of every request the server got
func (a *AuthServer) APIKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.apiKeys
}

// NewProtectedServer serves an MCP server at /mcp with one whoami tool that requires the authorization server's access tokens
// It points to the authorization server through its protected resource metadata, like servers following the 2025-06-18 spec
func NewProtectedServer(t *testing.T, as *AuthServer) *httptest.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "protected", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "whoami"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "you"}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"resource": srv.URL + "/mcp", "authorization_servers": []string{as.URL}, "scopes_supported": []string{"read"}})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !as.Valid(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// FollowAuthorization opens an authorization URL as the browser would and returns where the authorization server sends it back to
func FollowAuthorization(t *testing.T, authURL string) *url.URL {
	t.Helper()
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.Get(authURL) // #nosec G107 -- the URL points at the test's own authorization server
	if err != nil {
		t.Fatalf("failed to open the authorization URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("the authorization server answered %s instead of redirecting", resp.Status)
	}
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("invalid redirect: %v", err)
	}
	return back
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
