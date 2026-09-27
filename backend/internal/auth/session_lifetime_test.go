//go:build unit

package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// sessionStack serves the real auth and workspace routes behind the real auth middleware
type sessionStack struct {
	srv    *httptest.Server
	auth   *Module
	issuer *fakeIssuer
}

func newSessionStack(t *testing.T, workspacesEnabled bool, allowedGroups []string) *sessionStack {
	db := testutil.NewDatabaseForTest(t)
	ws := workspaces.New(workspaces.Dependencies{DB: db, Enabled: workspacesEnabled, AppURL: testAppURL})
	err := ws.EnsureDefault(t.Context(), workspacesEnabled)
	require.NoError(t, err)

	issuer := newFakeIssuer(t)
	m, err := New(Dependencies{
		DB:            db,
		Workspaces:    ws,
		EncryptionKey: []byte("unit-test-encryption-key"),
		Config:        Config{AppURL: testAppURL, Providers: []ProviderConfig{{Type: TypeOIDC, ID: "a", Name: "A", Issuer: issuer.URL, ClientID: "client-a", AllowedGroups: allowedGroups}}},
	})
	require.NoError(t, err)
	ws.SetSessions(m)

	mux := http.NewServeMux()
	api := httpserver.NewAPI(mux, SessionCookieName)
	authn := middleware.NewAuth(m, nil, SessionCookieName, testAppURL).Required()
	m.RegisterRoutes(api, authn, nil)
	ws.RegisterRoutes(api, authn)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &sessionStack{srv: srv, auth: m, issuer: issuer}
}

// post sends a same-origin write with the session cookie and returns the status and the session cookie it set, if any
func (s *sessionStack) post(t *testing.T, path, body, session string) (int, *http.Cookie) {
	req, err := http.NewRequest(http.MethodPost, s.srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	// #nosec G124 -- a request cookie carries only its name and value, so the attributes gosec wants don't apply
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session})
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == SessionCookieName {
			return res.StatusCode, c
		}
	}
	return res.StatusCode, nil
}

// expiryOf reads the expiry a session cookie value carries
func (s *sessionStack) expiryOf(t *testing.T, value string) time.Time {
	claims, err := s.auth.service.codec.parseSession(value)
	require.NoError(t, err)
	return time.Unix(claims.ExpiresAt, 0)
}

// requireSessionEndsBy checks that a re-issued session cookie ends no later than the session it replaced, both in its claims and in the browser
func (s *sessionStack) requireSessionEndsBy(t *testing.T, cookie *http.Cookie, deadline time.Time) {
	require.NotNil(t, cookie)
	require.LessOrEqual(t, s.expiryOf(t, cookie.Value).Unix(), deadline.Unix(), "the re-issued session outlives the sign-in it came from")
	require.LessOrEqual(t, cookie.MaxAge, int(time.Until(deadline).Seconds())+1, "the browser keeps the re-issued cookie past the session's end")
}

// signIn runs a whole login through the provider and returns the session cookie value
func (s *sessionStack) signIn(t *testing.T) (string, error) {
	svc := s.auth.service
	authURL, loginCookie, err := svc.BeginLogin(t.Context(), "a", "/")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	session, _, err := svc.FinishLogin(t.Context(), "a", loginCookie.Value, s.issuer.authorize(t, authURL), u.Query().Get("state"))
	if err != nil {
		return "", err
	}
	return session.Value, nil
}

// agedSession stands in for the user's session after most of its lifetime has passed, as the codec would have issued it at sign-in
func (s *sessionStack) agedSession(t *testing.T, session string, left time.Duration) (string, time.Time) {
	claims, err := s.auth.service.codec.parseSession(session)
	require.NoError(t, err)
	claims.ExpiresAt = time.Now().Add(left).Unix()
	value, err := s.auth.service.codec.encode(kindSession, claims)
	require.NoError(t, err)
	return value, time.Unix(claims.ExpiresAt, 0)
}

func TestSwitchingWorkspacesDoesNotExtendASession(t *testing.T) {
	s := newSessionStack(t, false, []string{"staff"})

	// The user signs in while the identity provider still lists them in an allowed group
	s.issuer.Groups = []string{"staff"}
	session, err := s.signIn(t)
	require.NoError(t, err)

	// The identity provider drops them, so they can't sign in anymore
	s.issuer.Groups = nil
	_, err = s.signIn(t)
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "got %v", err)

	// Their session is a minute from its end, the bound the docs promise for someone removed at the identity provider
	aged, deadline := s.agedSession(t, session, time.Minute)

	// Switching to the workspace they're already in must not hand them a new lifetime
	status, renewed := s.post(t, "/api/workspaces/"+workspaces.DefaultID+"/switch", "", aged)
	require.Equal(t, http.StatusOK, status)
	s.requireSessionEndsBy(t, renewed, deadline)
}

func TestCreatingAWorkspaceDoesNotExtendASession(t *testing.T) {
	s := newSessionStack(t, true, nil)
	session, err := s.signIn(t)
	require.NoError(t, err)
	aged, deadline := s.agedSession(t, session, time.Hour)

	// Creating a workspace moves the session into it without a sign-in, so it keeps the lifetime it had
	status, moved := s.post(t, "/api/workspaces", `{"name":"Side project"}`, aged)
	require.Equal(t, http.StatusOK, status)
	s.requireSessionEndsBy(t, moved, deadline)

	// Switching back keeps it too
	p, err := s.auth.VerifySession(t.Context(), aged)
	require.NoError(t, err)
	status, back := s.post(t, "/api/workspaces/"+p.WorkspaceID+"/switch", "", moved.Value)
	require.Equal(t, http.StatusOK, status)
	s.requireSessionEndsBy(t, back, deadline)
}
