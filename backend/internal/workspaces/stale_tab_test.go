//go:build unit

package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// workspaceHeader names the workspace a browser tab loaded, which the SPA sends so a write can't land in a workspace the tab doesn't show
const workspaceHeader = "X-Umpteenth-Workspace"

// plainSessions stands in for the auth module with readable cookies that name the user and the workspace, like the real session claims do
type plainSessions struct{ m *Module }

func (s plainSessions) SessionCookie(userID, workspaceID, _ string, _ time.Time) (http.Cookie, error) {
	// #nosec G124 -- the test server speaks plain HTTP, where a Secure cookie would never be sent back
	return http.Cookie{Name: "session", Value: userID + "." + workspaceID, Path: "/"}, nil
}

func (s plainSessions) VerifySession(ctx context.Context, value string) (principal.Principal, error) {
	userID, workspaceID, ok := strings.Cut(value, ".")
	if !ok {
		return principal.Principal{}, apperror.NotSignedIn()
	}
	access, err := s.m.Access(ctx, workspaceID, userID)
	if err != nil {
		return principal.Principal{}, err
	}
	return principal.Principal{UserID: userID, WorkspaceID: workspaceID, Role: access.Role, InstanceAdmin: access.InstanceAdmin}, nil
}

// tabs is one browser, whose tabs all share its cookie jar and so its session
type tabs struct {
	t      *testing.T
	base   string
	client *http.Client
}

// newBrowser serves the workspace routes behind the real auth middleware and signs a browser in as the user in the workspace
func newBrowser(t *testing.T, m *Module, userID, workspaceID string) *tabs {
	t.Helper()
	sessions := plainSessions{m}
	m.SetSessions(sessions)
	mux := http.NewServeMux()
	api := httpserver.NewAPI(mux, "session")
	m.RegisterRoutes(api, middleware.NewAuth(sessions, nil, "session", "https://umpteenth.example.com").Required())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	u, _ := url.Parse(srv.URL)
	cookie, _ := sessions.SessionCookie(userID, workspaceID, "", time.Now().Add(time.Hour))
	jar.SetCookies(u, []*http.Cookie{&cookie})
	return &tabs{t: t, base: srv.URL, client: &http.Client{Jar: jar}}
}

// send makes a same-origin request from one of the browser's tabs, which names the workspace the tab loaded when it has one
func (b *tabs) send(method, path, loadedWorkspace string, body any) (int, string) {
	b.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(b.t, err)
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, b.base+path, reader)
	require.NoError(b.t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if loadedWorkspace != "" {
		req.Header.Set(workspaceHeader, loadedWorkspace)
	}
	res, err := b.client.Do(req)
	require.NoError(b.t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// A switch in one tab moves the session cookie every tab shares, so a tab still showing the old workspace must not write to the new one
func TestStaleTabWritesDontReachTheWorkspaceItDoesntShow(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"delete", http.MethodDelete, "/api/workspace", nil},
		{"rename", http.MethodPatch, "/api/workspace", map[string]string{"name": "Renamed"}},
		{"invite a known user", http.MethodPost, "/api/workspace/invites", map[string]any{"email": "carl@example.com", "role": "admin", "expiresInDays": 7}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, db, _ := newTestModule(t, true)
			owner := seedUser(t, db, "Olivia Owner", "olivia@example.com")
			contractor := seedUser(t, db, "Carl Contractor", "carl@example.com")
			staging, err := m.Create(t.Context(), owner, "Staging")
			require.NoError(t, err)
			production, err := m.Create(t.Context(), owner, "Production")
			require.NoError(t, err)
			browser := newBrowser(t, m, owner, staging)

			// Tab 1 loads Staging, which the page then shows until it reloads
			status, body := browser.send(http.MethodGet, "/api/workspace", "", nil)
			require.Equal(t, http.StatusOK, status, body)
			var loaded workspaceDto
			require.NoError(t, json.Unmarshal([]byte(body), &loaded))
			require.Equal(t, "Staging", loaded.Name)

			// Tab 2 switches to Production, which rewrites the cookie tab 1 sends too
			status, body = browser.send(http.MethodPost, "/api/workspaces/"+production+"/switch", "", nil)
			require.Equal(t, http.StatusOK, status, body)

			// Tab 1 still shows Staging and writes there, so the write must be refused instead of landing in Production
			status, body = browser.send(c.method, c.path, loaded.ID, c.body)
			t.Logf("%s %s from the tab showing Staging -> %d %s", c.method, c.path, status, strings.TrimSpace(body))

			// Production is exactly as it was
			ws, err := m.Get(t.Context(), production)
			require.NoError(t, err, "Production was deleted by a tab showing Staging")
			require.Equal(t, "Production", ws.Name, "Production was renamed by a tab showing Staging")
			_, err = m.Access(t.Context(), production, contractor)
			require.Error(t, err, "the contractor got into Production through an invite sent from a tab showing Staging")

			// The tab learns that the session moved on, so it can reload instead of acting on what it shows
			require.Equal(t, http.StatusConflict, status, body)
			require.Contains(t, body, string(apperror.CodeWorkspaceChanged))
		})
	}
}

// Switching doesn't act on the session's workspace, so a tab that still shows a previous one may switch without reloading first
func TestStaleTabCanStillSwitchWorkspaces(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Olivia Owner", "olivia@example.com")
	staging, err := m.Create(t.Context(), owner, "Staging")
	require.NoError(t, err)
	production, err := m.Create(t.Context(), owner, "Production")
	require.NoError(t, err)
	browser := newBrowser(t, m, owner, staging)

	// Another tab moves the session to Production
	status, body := browser.send(http.MethodPost, "/api/workspaces/"+production+"/switch", staging, nil)
	require.Equal(t, http.StatusOK, status, body)

	// The tab that still shows Staging switches back and ends up there
	status, body = browser.send(http.MethodPost, "/api/workspaces/"+staging+"/switch", staging, nil)
	require.Equal(t, http.StatusOK, status, body)
	status, body = browser.send(http.MethodGet, "/api/workspace", staging, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"name":"Staging"`)

	// A write from the tab now reaches Staging, which it shows again
	status, body = browser.send(http.MethodPatch, "/api/workspace", staging, map[string]string{"name": "Staging 2"})
	require.Equal(t, http.StatusOK, status, body)
	ws, err := m.Get(t.Context(), staging)
	require.NoError(t, err)
	require.Equal(t, "Staging 2", ws.Name)
}
