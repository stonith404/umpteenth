//go:build unit

package docker

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// fakeEngine answers the engine calls of an attached exec and its kill exec
// The attached exec streams a fixed output right away and ends its stream once the kill exec ran, like a killed process tree
type fakeEngine struct {
	output   []string
	killed   chan struct{}
	killOnce sync.Once
	// mu guards conns, the hijacked streams that are closed when the test ends so no handler outlives it
	mu    sync.Mutex
	conns []io.Closer
}

func newFakeEngine(t *testing.T, output ...string) *client.Client {
	t.Helper()
	e := &fakeEngine{output: output, killed: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{version}/containers/{id}/exec", e.createExec)
	mux.HandleFunc("POST /{version}/exec/{id}/start", e.startExec)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		e.mu.Lock()
		for _, c := range e.conns {
			_ = c.Close()
		}
		e.mu.Unlock()
		srv.Close()
	})

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func (e *fakeEngine) createExec(w http.ResponseWriter, r *http.Request) {
	var opts container.ExecOptions
	_ = json.NewDecoder(r.Body).Decode(&opts)
	id := "process"
	if slices.Contains(opts.Cmd, "--kill") {
		id = "kill"
	}
	_ = json.NewEncoder(w).Encode(container.ExecCreateResponse{ID: id})
}

func (e *fakeEngine) startExec(w http.ResponseWriter, r *http.Request) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	e.mu.Lock()
	e.conns = append(e.conns, conn)
	e.mu.Unlock()
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")

	// The kill exec ends the attached process tree and then its own stream
	if r.PathValue("id") == "kill" {
		e.killOnce.Do(func() { close(e.killed) })
		return
	}

	// The process writes all of its output at once, and its stream ends when it is killed
	stdout := stdcopy.NewStdWriter(conn, stdcopy.Stdout)
	for _, line := range e.output {
		_, _ = io.WriteString(stdout, line)
	}
	<-e.killed
}

// A caller that stops reading stdout, like an MCP client whose reader gave up on a malformed line, must still be able to kill the process
func TestKillReturnsWhenStdoutIsNotDrained(t *testing.T) {
	a := &Adapter{cfg: Config{Runtime: defaultRuntime}, cli: newFakeEngine(t, "first\n", "second\n", "third\n"), log: slog.New(slog.DiscardHandler)}
	s := &containerSandbox{a: a, id: "sandbox", agentUser: sandbox.UserAgent}
	proc, err := s.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"server"}, User: sandbox.UserMCP})
	require.NoError(t, err)

	// Read the first line and leave the rest of the output unread
	buf := make([]byte, len("first\n"))
	_, err = io.ReadFull(proc.Stdout(), buf)
	require.NoError(t, err)
	require.Equal(t, "first\n", string(buf))

	// Kill cuts off the unread output once the stream grace period is over
	killed := make(chan struct{})
	go func() {
		_ = proc.Kill()
		close(killed)
	}()
	select {
	case <-killed:
	case <-time.After(streamGrace + 2*exitCodeWait):
		t.Fatal("Kill never returned, since the output nobody reads keeps the stream open")
	}

	// The unread output is gone instead of blocking a later reader
	_, err = io.ReadAll(proc.Stdout())
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}
