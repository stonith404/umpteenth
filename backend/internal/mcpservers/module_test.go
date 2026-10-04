//go:build unit

package mcpservers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers/mcpserversdb"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// denyAll is a limiter the workspace has used up
type denyAll struct{}

func (denyAll) Allow(context.Context, string) (bool, time.Duration, error) {
	return false, 10 * time.Second, nil
}

// countingAdapter counts the sandboxes it is asked to create
type countingAdapter struct {
	*fake.Adapter
	created int
}

func (c *countingAdapter) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.created++
	return c.Adapter.Create(ctx, spec)
}

func TestStdioTestsAreRateLimitedBeforeTheSandboxStarts(t *testing.T) {
	m, ctx, _ := newTestModule(t)
	adapter := &countingAdapter{Adapter: fake.New()}
	m.deps.Adapter = adapter
	m.deps.DefaultImage = func(context.Context, string) string { return "img" }
	m.deps.TestLimiter = denyAll{}

	// A caller over the limit starts no test sandbox
	stdio := addServer(t, m, ctx, serverBody{Name: "local", Transport: "stdio", Command: "npx"})
	_, err := m.test(ctx, &idInput{ID: stdio.ID})
	require.True(t, apperror.IsCode(err, apperror.CodeRateLimited), err)
	require.Zero(t, adapter.created)

	// An HTTP server needs no sandbox, so its test isn't limited
	remote := addServer(t, m, ctx, serverBody{Name: "remote", Transport: "http", URL: "http://127.0.0.1:1/mcp"})
	out, err := m.test(ctx, &idInput{ID: remote.ID})
	require.NoError(t, err)
	require.False(t, out.Body.OK)
}

func TestStaleServerUpdateCannotRebindANewLogin(t *testing.T) {
	m, ctx, wid := newTestModule(t)
	server := addServer(t, m, ctx, serverBody{Name: "original", Transport: "http", URL: "http://127.0.0.1:1/mcp"})
	before, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: server.ID})
	require.NoError(t, err)

	// Another request moves the server and finishes its login before the stale update writes
	testutil.Exec(t, m.db, "UPDATE mcp_servers SET url = $1, oauth_credentials = $2, oauth_logged_in_at = $3 WHERE id = $4", "https://replacement.example/mcp", "replacement-login", 2, server.ID)
	n, err := m.queries.UpdateServer(ctx, mcpserversdb.UpdateServerParams{
		WorkspaceID: wid, ID: server.ID, Name: "stale", Transport: before.Transport, Url: before.Url,
		Args: before.Args, Env: before.Env, Headers: before.Headers, OauthConfig: before.OauthConfig, Enabled: before.Enabled,
		PreviousTransport: before.Transport, PreviousUrl: new(deref(before.Url)), PreviousOauthConfig: before.OauthConfig,
	})
	require.NoError(t, err)
	require.Zero(t, n)
	after, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: server.ID})
	require.NoError(t, err)
	require.Equal(t, "https://replacement.example/mcp", deref(after.Url))
	require.Equal(t, "replacement-login", string(after.OauthCredentials))
}
