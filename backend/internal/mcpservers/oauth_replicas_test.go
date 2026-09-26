//go:build unit

package mcpservers

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/mcp/mcptest"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// runEvents collects what a run was told about its MCP servers
type runEvents struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *runEvents) emit(e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *runEvents) errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if e.Type == events.TypeError {
			out = append(out, fmt.Sprint(e.Payload.(map[string]any)["message"]))
		}
	}
	return out
}

func TestReplicasRefreshingTogetherKeepTheLogin(t *testing.T) {
	as := mcptest.NewAuthServer(t)
	srv := mcptest.NewProtectedServer(t, as)

	// The first refresh grant is answered by the authorization server, which rotates the refresh token, but its response stays on the wire until the test lets it go
	// That is the round trip in which a run on another replica can load the same refresh token
	firstHeld, release := make(chan struct{}), make(chan struct{})
	var refreshes int
	var gateMu sync.Mutex
	inner := as.Config.Handler
	as.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			inner.ServeHTTP(w, r)
			return
		}
		_ = r.ParseForm()
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		if r.PostForm.Get("grant_type") == "refresh_token" {
			gateMu.Lock()
			refreshes++
			first := refreshes == 1
			gateMu.Unlock()
			if first {
				close(firstHeld)
				<-release
			}
		}
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})

	// Two replicas share one database, each with its own per-process token cache
	replicaA, ctx, wid := newTestModule(t)
	replicaB, err := New(Dependencies{DB: replicaA.db, Egress: egress.New(true), Secrets: plainSecrets{}, EncryptionKey: []byte("test-encryption-key"), AppURL: "https://app.example.com"})
	require.NoError(t, err)

	// Two jobs use the same OAuth server, and the login's access token is already due for a refresh when their runs start
	server := addServer(t, replicaA, ctx, serverBody{Name: "protected", Transport: "http", URL: srv.URL + "/mcp"})
	jobA, jobB := testutil.SeedJob(t, replicaA.db, wid, "skip"), testutil.SeedJob(t, replicaA.db, wid, "skip")
	testutil.Exec(t, replicaA.db, "INSERT INTO job_mcp_servers (job_id, mcp_server_id) VALUES ($1, $2), ($3, $2)", jobA, server.ID, jobB)
	as.AccessTTL = 1
	started, err := replicaA.login(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	_, err = replicaA.callback(ctx, callbackFrom(server.ID, mcptest.FollowAuthorization(t, started.Body.AuthorizationURL)))
	require.NoError(t, err)
	as.AccessTTL = 3600

	// Replica A starts its run and spends the refresh token
	var eventsA, eventsB runEvents
	doneA := make(chan []int, 1)
	go func() {
		tools, cleanup, err := replicaA.ToolsForRun(ctx, runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobA}, Emit: eventsA.emit})
		assert.NoError(t, err)
		if cleanup != nil {
			cleanup()
		}
		doneA <- []int{len(tools)}
	}()
	select {
	case <-firstHeld:
	case <-time.After(30 * time.Second):
		t.Fatal("replica A never refreshed the token")
	}

	// Replica B starts its run while A's refresh is in flight
	doneB := make(chan []int, 1)
	go func() {
		tools, cleanup, err := replicaB.ToolsForRun(ctx, runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobB}, Emit: eventsB.emit})
		assert.NoError(t, err)
		if cleanup != nil {
			cleanup()
		}
		doneB <- []int{len(tools)}
	}()

	// A's response arrives once B is done, or after a while when B waits for A
	var toolsB []int
	select {
	case toolsB = <-doneB:
	case <-time.After(2 * time.Second):
	}
	close(release)
	toolsA := <-doneA
	if toolsB == nil {
		toolsB = <-doneB
	}
	t.Logf("replica A run: %d tools, errors %q", toolsA[0], eventsA.errors())
	t.Logf("replica B run: %d tools, errors %q", toolsB[0], eventsB.errors())
	require.Equal(t, 1, toolsA[0], "replica A refreshed the token and gets the tools")

	// A refresh on one replica must not end the login for everyone, since the authorization server just issued a fresh refresh token
	got, err := replicaA.get(ctx, &idInput{ID: server.ID})
	require.NoError(t, err)
	assert.Equal(t, authOAuth, got.Body.Auth.Status, "the login survives both refreshes")
	assert.NotNil(t, got.Body.Auth.LoggedInAt, "the login survives both refreshes")
	assert.Equal(t, 1, toolsB[0], "replica B uses the token replica A refreshed instead of reporting an expired login")

	// Spending a rotated refresh token twice makes servers with reuse detection revoke the whole login, so only one replica may spend it
	gateMu.Lock()
	assert.Equal(t, 1, refreshes, "only one replica spends the refresh token")
	gateMu.Unlock()

	// Later runs on either replica still get the server's tools without anyone logging in again
	var later runEvents
	tools, cleanup, err := replicaB.ToolsForRun(ctx, runner.RunContext{Run: runner.Run{WorkspaceID: wid, JobID: jobB}, Emit: later.emit})
	require.NoError(t, err)
	cleanup()
	assert.Len(t, tools, 1, "a later run still gets the tools, errors %q", later.errors())
}
