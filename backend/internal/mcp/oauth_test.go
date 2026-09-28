//go:build unit

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/mcp/mcptest"
)

// memoryStore keeps a login in memory, standing in for the database
type memoryStore struct {
	mu      sync.Mutex
	creds   *OAuthCredentials
	saves   int
	expired bool
	// saveErrs fail the next saves, one error each
	saveErrs []error
	// beforeSave runs at the start of every save
	beforeSave func()
}

func (s *memoryStore) Load(context.Context) (OAuthCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		return OAuthCredentials{}, ErrLoginExpired
	}
	return *s.creds, nil
}

func (s *memoryStore) Lock(context.Context) (func(), error) {
	return func() {}, nil
}

func (s *memoryStore) Save(ctx context.Context, creds OAuthCredentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeSave != nil {
		s.beforeSave()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(s.saveErrs) > 0 {
		err := s.saveErrs[0]
		s.saveErrs = s.saveErrs[1:]
		return err
	}
	s.creds = &creds
	s.saves++
	return nil
}

func (s *memoryStore) Expire(context.Context, OAuthCredentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds = nil
	s.expired = true
	return nil
}

// login runs a whole login against the fake authorization server, following its redirect as the browser would
func login(t *testing.T, m *Manager, serverURL string, req OAuthLoginRequest) OAuthCredentials {
	t.Helper()
	req.ServerURL = serverURL
	req.RedirectURL = "https://app.example.com/api/mcp-servers/s1/oauth/callback"
	authURL, pending, err := m.BeginOAuthLogin(t.Context(), req)
	require.NoError(t, err)

	q := mcptest.FollowAuthorization(t, authURL).Query()
	creds, err := m.FinishOAuthLogin(t.Context(), pending, q.Get("state"), q.Get("code"), q.Get("iss"))
	require.NoError(t, err)
	return creds
}

func TestDiscoverOAuthFollowsTheChallenge(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))

	meta, err := m.DiscoverOAuth(t.Context(), srv.URL+"/mcp", nil)
	require.NoError(t, err)
	require.NotNil(t, meta)
	require.Equal(t, srv.URL+"/mcp", meta.Resource)
	require.Equal(t, as.URL+"/token", meta.TokenEndpoint)
	require.Equal(t, as.URL+"/register", meta.RegistrationEndpoint)
	require.Equal(t, []string{"read"}, meta.Scopes, "the resource's scopes win over everything the authorization server supports")
	require.True(t, meta.OfflineAccess)
}

func TestDiscoverOAuthFindsLegacyServers(t *testing.T) {
	// A 2025-03-26 server is its own authorization server and publishes no resource metadata
	as := mcptest.NewAuthServer(t)
	m := NewManager(egress.New(true))

	meta, err := m.DiscoverOAuth(t.Context(), as.URL+"/mcp", nil)
	require.NoError(t, err)
	require.NotNil(t, meta)
	require.Empty(t, meta.Resource)
	require.Equal(t, []string{"read", "write", "offline_access"}, meta.Scopes)
}

func TestDiscoverOAuthReportsServersWithoutLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)
	m := NewManager(egress.New(true))

	meta, err := m.DiscoverOAuth(t.Context(), srv.URL+"/mcp", nil)
	require.NoError(t, err)
	require.Nil(t, meta)
}

func TestDiscoverOAuthSendsHeadersToTheServerOnly(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	var mu sync.Mutex
	var serverKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		serverKeys = append(serverKeys, r.Header.Get("X-Api-Key"))
		mu.Unlock()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			mcptest.WriteJSON(w, http.StatusOK, map[string]any{"authorization_servers": []string{as.URL}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	m := NewManager(egress.New(true))

	meta, err := m.DiscoverOAuth(t.Context(), srv.URL, map[string]string{"X-Api-Key": "key"})
	require.NoError(t, err)
	require.NotNil(t, meta)
	require.NotEmpty(t, serverKeys)
	for _, key := range serverKeys {
		require.Equal(t, "key", key)
	}
	require.NotEmpty(t, as.APIKeys(), "discovery reached the authorization server")
	for _, key := range as.APIKeys() {
		require.Empty(t, key, "the server's headers never reach a third-party authorization server")
	}
}

func TestOAuthLoginConnectsAndCallsTools(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))

	// Without a login the connection is turned away as unauthorized
	_, err := m.Connect(t.Context(), ServerConfig{Name: "p", Transport: TransportHTTP, URL: srv.URL + "/mcp"}, nil)
	require.ErrorIs(t, err, ErrUnauthorized)

	// The login registers a client, asks for the resource's scopes plus offline_access and binds the tokens to the resource
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})
	authorize := as.LastAuthorize()
	require.Equal(t, "read offline_access", authorize.Get("scope"))
	require.Equal(t, srv.URL+"/mcp", authorize.Get("resource"))
	require.Equal(t, "S256", authorize.Get("code_challenge_method"))
	require.Equal(t, "none", creds.Client.AuthMethod)
	require.NotEmpty(t, creds.RefreshToken)
	require.NotZero(t, creds.ExpiresAt)

	store := &memoryStore{creds: &creds}
	session, err := m.Connect(t.Context(), ServerConfig{Name: "p", Transport: TransportHTTP, URL: srv.URL + "/mcp", OAuth: m.NewOAuthTokens(creds, store)}, nil)
	require.NoError(t, err)
	t.Cleanup(session.Close)
	out, isError, err := session.Call(t.Context(), "whoami", nil)
	require.NoError(t, err)
	require.False(t, isError)
	require.Equal(t, "you", out)
}

func TestExpiringTokensAreRefreshedAndStored(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.AccessTTL = 10
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})
	require.True(t, creds.NeedsRefresh(time.Now()), "a token within the refresh skew is refreshed before use")

	store := &memoryStore{creds: &creds}
	access, err := m.NewOAuthTokens(creds, store).Token(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, creds.AccessToken, access)
	require.Equal(t, 1, store.saves)
	require.Equal(t, access, store.creds.AccessToken)
	require.NotEqual(t, creds.RefreshToken, store.creds.RefreshToken, "the rotated refresh token is kept")
}

func TestRefreshedTokensAreStoredDespiteAFailedSave(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.AccessTTL = 10
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})

	// The authorization server rotated the refresh token, so a save that fails once is tried again instead of losing it
	store := &memoryStore{creds: &creds, saveErrs: []error{errors.New("database is locked")}}
	access, err := m.NewOAuthTokens(creds, store).Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, store.saves)
	require.Equal(t, access, store.creds.AccessToken)
	require.NotEqual(t, creds.RefreshToken, store.creds.RefreshToken)

	// A run canceled right after the refresh still stores the rotated refresh token
	ctx, cancel := context.WithCancel(t.Context())
	store.beforeSave = cancel
	_, err = m.NewOAuthTokens(*store.creds, store).Token(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, store.saves)
}

func TestRefreshOfALoginLoggedOutMeanwhileIsNotUsed(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.AccessTTL = 10
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})

	// The store finds no login to save the tokens on, since it was logged out while the refresh was on the wire
	store := &memoryStore{creds: &creds, saveErrs: []error{ErrLoginExpired}}
	_, err := m.NewOAuthTokens(creds, store).Token(t.Context())
	require.ErrorIs(t, err, ErrLoginExpired)
	require.Zero(t, store.saves)
}

func TestRefreshUsesATokenAnotherReplicaStored(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.AccessTTL = 10
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})

	// Another replica refreshed and stored a token that is good for an hour, so this one uses it instead of spending the refresh token again
	newer := creds
	newer.AccessToken, newer.ExpiresAt = "from-elsewhere", time.Now().Add(time.Hour).UnixMilli()
	store := &memoryStore{creds: &newer}
	access, err := m.NewOAuthTokens(creds, store).Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, "from-elsewhere", access)
	require.Zero(t, as.Refreshes())
}

func TestRejectedTokenIsRefreshedAndTheRequestRetried(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})

	// The access token dies long before its expiry, which only a 401 reveals
	as.Revoke()
	store := &memoryStore{creds: &creds}
	session, err := m.Connect(t.Context(), ServerConfig{Name: "p", Transport: TransportHTTP, URL: srv.URL + "/mcp", OAuth: m.NewOAuthTokens(creds, store)}, nil)
	require.NoError(t, err)
	t.Cleanup(session.Close)
	require.Equal(t, 1, as.Refreshes())
	require.Equal(t, 1, store.saves)
}

func TestRevokedRefreshTokenEndsTheLogin(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.AccessTTL = 10
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))
	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{})
	creds.RefreshToken = "revoked"

	store := &memoryStore{creds: &creds}
	_, err := m.Connect(t.Context(), ServerConfig{Name: "p", Transport: TransportHTTP, URL: srv.URL + "/mcp", OAuth: m.NewOAuthTokens(creds, store)}, nil)
	require.ErrorIs(t, err, ErrLoginExpired)
	require.True(t, store.expired)
}

func TestConfiguredClientSkipsRegistration(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.WithoutRegistration = true
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))

	// Without dynamic registration the login needs a client ID, like Codex's --oauth-client-id
	_, _, err := m.BeginOAuthLogin(t.Context(), OAuthLoginRequest{ServerURL: srv.URL + "/mcp", RedirectURL: "https://app.example.com/cb"})
	require.ErrorContains(t, err, "client ID")

	creds := login(t, m, srv.URL+"/mcp", OAuthLoginRequest{Client: OAuthClient{ID: "preregistered"}, Scopes: []string{"write"}})
	require.Equal(t, "preregistered", creds.Client.ID)
	require.Equal(t, "write offline_access", as.LastAuthorize().Get("scope"))
	require.Zero(t, as.Clients())
}

func TestLoginWithoutScopesRequestsNone(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m := NewManager(egress.New(true))

	authURL, pending, err := m.BeginOAuthLogin(t.Context(), OAuthLoginRequest{ServerURL: srv.URL + "/mcp", RedirectURL: "https://app.example.com/cb", WithoutScopes: true})
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	require.False(t, u.Query().Has("scope"))
	require.False(t, pending.ScopesDiscovered)
}

func TestFinishRejectsAMismatchedResponse(t *testing.T) {
	m := NewManager(egress.New(true))
	pending := OAuthLogin{State: "state", Issuer: "https://as.example.com", IssRequired: true}

	_, err := m.FinishOAuthLogin(t.Context(), pending, "other", "code", "https://as.example.com")
	require.ErrorContains(t, err, "state")
	_, err = m.FinishOAuthLogin(t.Context(), pending, "state", "code", "")
	require.ErrorContains(t, err, "identify")
	_, err = m.FinishOAuthLogin(t.Context(), pending, "state", "code", "https://evil.example.com")
	require.ErrorContains(t, err, "issuer")
}

func TestResourceMetadataMustCoverTheServer(t *testing.T) {
	base, _ := url.Parse("https://mcp.example.com/v1/mcp")
	require.True(t, resourceCovers("", base))
	require.True(t, resourceCovers("https://mcp.example.com", base))
	require.True(t, resourceCovers("https://mcp.example.com/v1/mcp", base))
	require.True(t, resourceCovers("https://mcp.example.com/v1/", base))
	require.False(t, resourceCovers("https://mcp.example.com/v2", base))
	require.False(t, resourceCovers("https://other.example.com/v1/mcp", base))
}

func TestUnionKeepsTheFirstOfEachScope(t *testing.T) {
	require.Equal(t, []string{"read", "write", "admin"}, union([]string{"read", " write", ""}, []string{"write", "admin", "read "}))
	require.Nil(t, union(nil, []string{" "}))

	// A metadata document full of distinct scopes is deduplicated in linear time
	many := make([]string, 200_000)
	for i := range many {
		many[i] = "s" + strconv.Itoa(i)
	}
	require.Len(t, union(many, many), len(many))
}
