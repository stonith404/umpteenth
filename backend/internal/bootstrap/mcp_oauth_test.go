//go:build unit

package bootstrap

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/mcpapi"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// mcpResource is the MCP endpoint of an instance with the default app.url, which the access tokens are issued for
const mcpResource = "http://localhost:8080/api/mcp"

// testIssuer is an OpenID provider that issues access tokens for whichever user and audience a test names, as Pocket ID does for an API resource
type testIssuer struct {
	*httptest.Server
	key *rsa.PrivateKey
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	i := &testIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": i.URL, "authorization_endpoint": i.URL + "/authorize", "token_endpoint": i.URL + "/token", "jwks_uri": i.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key", Algorithm: "RS256", Use: "sig"}}})
	})
	i.Server = httptest.NewServer(mux)
	t.Cleanup(i.Close)
	return i
}

// accessToken signs an access token for the subject, issued for the audience
func (i *testIssuer) accessToken(t *testing.T, subject, audience string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: i.key, KeyID: "key"}}, (&jose.SignerOptions{}).WithType("at+jwt"))
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{"iss": i.URL, "sub": subject, "aud": audience, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()})
	require.NoError(t, err)
	signed, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := signed.CompactSerialize()
	require.NoError(t, err)
	return token
}

// get sends a GET request, whose body the caller closes
func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// withOAuth lets MCP clients sign in through the issuer, configured as the pocket-id sign-in provider
func withOAuth(issuer *testIssuer) func(cfg *config.Config) {
	return func(cfg *config.Config) {
		cfg.Auth.Providers = map[string]*config.AuthProvider{"pocket-id": {Type: "oidc", Name: "Pocket ID", Issuer: issuer.URL, ClientID: "umpteenth"}}
		cfg.MCP.OAuthProvider = "pocket-id"
	}
}

func TestMCPTellsClientsWhereToSignIn(t *testing.T) {
	issuer := newTestIssuer(t)
	s := newMCPTestServer(t, withOAuth(issuer))

	// The protected resource metadata names the identity provider, at the path RFC 9728 derives from the endpoint and at the bare prefix
	for _, path := range []string{"/.well-known/oauth-protected-resource/api/mcp", "/.well-known/oauth-protected-resource"} {
		resp := get(t, s.url+path)
		var metadata struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&metadata))
		_ = resp.Body.Close()
		assert.Equal(t, mcpResource, metadata.Resource, path)
		assert.Equal(t, []string{issuer.URL}, metadata.AuthorizationServers, path)
	}

	// A request without a token learns where the metadata is
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+mcpapi.Path, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Bearer resource_metadata="http://localhost:8080/.well-known/oauth-protected-resource/api/mcp"`, resp.Header.Get("WWW-Authenticate"))
}

func TestMCPWithoutOAuthServesNoMetadata(t *testing.T) {
	s := newMCPTestServer(t)
	resp := get(t, s.url+"/.well-known/oauth-protected-resource/api/mcp")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMCPActsAsTheUserBehindAnAccessToken(t *testing.T) {
	issuer := newTestIssuer(t)
	s := newMCPTestServer(t, withOAuth(issuer))

	// Alice signed in to Umpteenth through the provider before, which created her account
	alice := database.NewID()
	testutil.Exec(t, s.db, "INSERT INTO users (id, issuer, subject, email, name, created_at) VALUES ($1, $2, $3, $4, $5, $6)", alice, issuer.URL, "alice", "alice@example.com", "Alice", database.Now())
	testutil.SeedMember(t, s.db, s.workspaceID, alice, "member")

	// Her agent creates a job, which is hers like one she created in the browser
	session := s.connectWith(t, issuer.accessToken(t, "alice", mcpResource))
	text, isError := call(t, session, "create_job", map[string]any{"name": "Alice's report", "instruction": "Summarize the week", "network": "none"})
	require.False(t, isError, text)
	var job struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &job))
	var workspaceID string
	var createdBy *string
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT workspace_id, created_by FROM jobs WHERE id = $1", job.ID).Scan(&workspaceID, &createdBy))
	assert.Equal(t, s.workspaceID, workspaceID)
	require.NotNil(t, createdBy)
	assert.Equal(t, alice, *createdBy)

	// API tokens keep working next to OAuth
	text, isError = call(t, s.connect(t), "list_jobs", map[string]any{})
	require.False(t, isError, text)
	assert.Contains(t, text, job.ID)

	// A token for another resource or of someone who never signed in is refused, with a message the client can show
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	status, _ := s.post(t, issuer.accessToken(t, "alice", "https://other.example.com/mcp"), initialize)
	assert.Equal(t, http.StatusUnauthorized, status)
	status, body := s.post(t, issuer.accessToken(t, "bob", mcpResource), initialize)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, body, "Sign in to Umpteenth in the browser once")

	// A workspace she isn't a member of stays closed, even when the client names it
	other := testutil.SeedWorkspace(t, s.db)
	status, body = s.post(t, issuer.accessToken(t, "alice", mcpResource), initialize, "X-Umpteenth-Workspace", other)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, body, "no access to this workspace")
}
