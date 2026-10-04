//go:build unit

package mcpservers

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/mcp/mcptest"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers/mcpserversdb"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// plainSecrets leaves values as they are, since these tests reference no secrets
type plainSecrets struct{}

func (plainSecrets) Expand(_ context.Context, _, value string) (string, error) { return value, nil }

// newTestModule returns the module on a fresh database and a context signed in as a user of its workspace
func newTestModule(t *testing.T) (*Module, context.Context, string) {
	t.Helper()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	m, err := New(Dependencies{DB: db, Egress: egress.New(true), Secrets: plainSecrets{}, EncryptionKey: []byte("test-encryption-key"), AppURL: "https://app.example.com"})
	require.NoError(t, err)
	return m, principal.WithPrincipal(t.Context(), principal.Principal{UserID: "u1", WorkspaceID: wid}), wid
}

// callbackFrom turns where the authorization server sent the browser into the callback's input
func callbackFrom(id string, back *url.URL) *callbackInput {
	q := back.Query()
	return &callbackInput{ID: id, Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss"), Error: q.Get("error"), ErrorDescription: q.Get("error_description")}
}

func addServer(t *testing.T, m *Module, ctx context.Context, body serverBody) serverDto {
	t.Helper()
	out, err := m.create(ctx, &createInput{Body: body})
	require.NoError(t, err)
	return out.Body
}

func TestOAuthLoginThroughTheCallback(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, _ := newTestModule(t)

	// Adding the server detects its OAuth login, which the UI then starts like codex mcp add does
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})
	require.Equal(t, authNotLoggedIn, server.Auth.Status)

	// The browser goes to the authorization server and comes back to this server's own callback
	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	back := mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)
	require.Equal(t, "https://app.example.com/api/mcp-servers/"+server.ID+"/oauth/callback", back.Scheme+"://"+back.Host+back.Path)
	done, err := m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Equal(t, "/mcp?oauth=success&server="+server.ID, done.Location)

	got, err := m.get(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	require.Equal(t, authOAuth, got.Body.Auth.Status)
	require.True(t, got.Body.Auth.Refreshable)
	require.NotNil(t, got.Body.Auth.LoggedInAt)

	// Connections use the login
	tested, err := m.test(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	require.True(t, tested.Body.OK, tested.Body.Error)
	require.Len(t, tested.Body.Tools, 1)

	// The callback URL works only once
	replayed, err := m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Contains(t, replayed.Location, "oauthError=")

	// After logging out the test says the server needs a login again
	_, err = m.logout(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	tested, err = m.test(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	require.False(t, tested.Body.OK)
	require.Contains(t, tested.Body.Error, "requires authorization")
	require.Equal(t, authNotLoggedIn, tested.Body.Auth.Status)
}

func TestCallbackBelongsToTheUserWhoStartedTheLogin(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, wid := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})

	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	back := mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)

	// A callback URL that reaches another user's browser doesn't log the server in
	other := principal.WithPrincipal(t.Context(), principal.Principal{UserID: "u2", WorkspaceID: wid})
	done, err := m.callback(other, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Contains(t, done.Location, "oauthError=")
	got, err := m.get(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	require.Equal(t, authNotLoggedIn, got.Body.Auth.Status)

	// Nor does it use up the login, which the user who started it can still finish
	done, err = m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Equal(t, "/mcp?oauth=success&server="+server.ID, done.Location)
}

func TestRejectedScopesAreRetriedWithoutScopes(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.RejectScopes = true
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, _ := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})

	// The provider refuses the discovered scopes, so the callback starts a second login without them, like Codex
	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	refused, err := m.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(refused.Location, as.URL+"/authorize?"), refused.Location)
	retry, err := url.Parse(refused.Location)
	require.NoError(t, err)
	require.False(t, retry.Query().Has("scope"))

	done, err := m.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, refused.Location)))
	require.NoError(t, err)
	require.Equal(t, "/mcp?oauth=success&server="+server.ID, done.Location)
}

func TestConfiguredScopesAreNotRetried(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	as.RejectScopes = true
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, _ := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp", OAuth: &oauthConfig{Scopes: []string{"write"}}})

	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	done, err := m.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)))
	require.NoError(t, err)
	require.Contains(t, done.Location, "oauthError=")
	require.Contains(t, done.Location, "invalid_scope")
}

func TestChangingTheURLDropsTheLogin(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, _ := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})
	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	_, err = m.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)))
	require.NoError(t, err)

	// Renaming keeps the login, pointing the server elsewhere doesn't
	renamed, err := m.update(ctx, &updateInput{ID: server.ID, Body: serverBody{Name: "renamed", Transport: "http", URL: srv.URL + "/mcp"}})
	require.NoError(t, err)
	require.Equal(t, authOAuth, renamed.Body.Auth.Status)
	moved, err := m.update(ctx, &updateInput{ID: server.ID, Body: serverBody{Name: "renamed", Transport: "http", URL: as.URL + "/mcp"}})
	require.NoError(t, err)
	require.Nil(t, moved.Body.Auth.LoggedInAt)
	require.Equal(t, authNotLoggedIn, moved.Body.Auth.Status, "the new URL is detected again")
}

func TestALoginOnlyLandsOnTheURLItWasStartedFor(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	elsewhere := mcptest.NewProtectedServer(t, as)
	m, ctx, wid := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})

	// Renaming the server while the browser is at the authorization server keeps the login
	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	back := mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)
	_, err = m.update(ctx, &updateInput{ID: server.ID, Body: serverBody{Name: "renamed", Transport: "http", URL: srv.URL + "/mcp"}})
	require.NoError(t, err)
	done, err := m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Equal(t, "/mcp?oauth=success&server="+server.ID, done.Location)

	// Pointing it elsewhere meanwhile drops the started login, so its callback finishes nothing
	started, err = m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	back = mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)
	sealed, err := m.queries.GetOAuthPending(ctx, mcpserversdb.GetOAuthPendingParams{WorkspaceID: wid, ID: server.ID})
	require.NoError(t, err)
	_, err = m.update(ctx, &updateInput{ID: server.ID, Body: serverBody{Name: "renamed", Transport: "http", URL: elsewhere.URL + "/mcp"}})
	require.NoError(t, err)
	done, err = m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Contains(t, done.Location, "oauthError=")

	// A callback that took the started login just before the change doesn't store its token on the moved server either
	require.NoError(t, m.queries.SetOAuthPending(ctx, mcpserversdb.SetOAuthPendingParams{WorkspaceID: wid, ID: server.ID, OauthPending: sealed}))
	done, err = m.callback(ctx, callbackFrom(server.ID, back))
	require.NoError(t, err)
	require.Contains(t, done.Location, "changed")
	got, err := m.get(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	require.Nil(t, got.Body.Auth.LoggedInAt)
	require.Equal(t, authNotLoggedIn, got.Body.Auth.Status)
}

func TestAuthorizationHeaderWinsOverOAuth(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, _ := newTestModule(t)

	server := addServer(t, m, ctx, serverBody{Name: "token", Transport: "http", URL: srv.URL + "/mcp", Headers: map[string]string{"authorization": "Bearer {{secret:TOKEN}}"}})
	require.Equal(t, authBearerToken, server.Auth.Status)

	stdio := addServer(t, m, ctx, serverBody{Name: "local", Transport: "stdio", Command: "npx", OAuth: &oauthConfig{ClientID: "ignored"}})
	require.Equal(t, authUnsupported, stdio.Auth.Status)
	require.Empty(t, stdio.OAuth.ClientID, "OAuth settings are only kept for HTTP servers")
}

func TestRunsUseTheLoginAndReportAMissingOne(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)
	m, ctx, wid := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})
	jobID := testutil.SeedJob(t, m.db, wid, "skip")
	testutil.Exec(t, m.db, "INSERT INTO job_mcp_servers (job_id, mcp_server_id) VALUES ($1, $2)", jobID, server.ID)

	var emitted []events.Event
	rc := runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobID}, Emit: func(e events.Event) { emitted = append(emitted, e) }}

	// Without a login the run is told why the server's tools are missing
	tools, cleanup, err := m.ToolsForRun(ctx, rc)
	require.NoError(t, err)
	cleanup()
	require.Empty(t, tools)
	require.Len(t, emitted, 1)
	require.Contains(t, emitted[0].Payload.(map[string]any)["message"], "requires authorization")

	// With a login the agent gets the server's tools and can call them
	started, err := m.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	_, err = m.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)))
	require.NoError(t, err)
	tools, cleanup, err = m.ToolsForRun(ctx, rc)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.Len(t, tools, 1)
	result := tools[0].Run(ctx, llm.ToolCall{Name: tools[0].Def().Name, Args: json.RawMessage(`{}`)})
	require.False(t, result.IsError, result.Content)
	require.Equal(t, "you", result.Content)
}

func TestAnActiveStoreCannotReadAReplacementLogin(t *testing.T) {
	m, ctx, wid := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "original", Transport: "http", URL: "http://127.0.0.1:1/mcp"})
	store := oauthStore{m: m, workspaceID: wid, serverID: server.ID, loggedInAt: 1}
	testutil.Exec(t, m.db, "UPDATE mcp_servers SET oauth_credentials = $1, oauth_logged_in_at = $2 WHERE id = $3", "replacement-login", 2, server.ID)
	_, err := store.Load(ctx)
	require.ErrorIs(t, err, mcp.ErrLoginExpired)
}
