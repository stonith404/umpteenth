//go:build unit

package bootstrap

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

func TestHTTPServiceEndsOpenStreamsOnShutdown(t *testing.T) {
	// The stream behaves like the SSE endpoints: it answers, flushes and then waits until its stream context ends
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := httpserver.StreamContext(r.Context())
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(": connected\n\n"))
		w.(http.Flusher).Flush()
		<-ctx.Done()
	})

	// An ordinary request that is still in flight when shutdown starts must be allowed to finish
	started := make(chan struct{})
	release := make(chan struct{})
	releaseSlow := sync.OnceFunc(func() { close(release) })
	defer releaseSlow()
	mux.HandleFunc("/api/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		if r.Context().Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("done"))
	})

	// The listener wrapper reports the port the service picked, so the test needs no fixed port
	addrCh := make(chan string, 1)
	wrap := func(ln net.Listener) net.Listener {
		addrCh <- ln.Addr().String()
		return ln
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- httpService("127.0.0.1:0", mux, wrap)(ctx)
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case err := <-done:
		require.FailNow(t, "service stopped before listening", "%v", err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "service did not start listening")
	}

	// Open a stream the way the UI keeps /api/events open, and wait until the server is serving it
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/events", nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	stream := bufio.NewReader(res.Body)
	line, err := stream.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, ": connected\n", line)

	// Start an ordinary request, which is still running when shutdown starts
	slow := make(chan string, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/slow", nil)
		if err != nil {
			slow <- err.Error()
			return
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			slow <- err.Error()
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		slow <- res.Status + " " + string(body)
	}()
	<-started

	// Stopping the service ends the open stream right away instead of waiting out the shutdown deadline and failing
	ended := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(stream)
		ended <- err
	}()
	start := time.Now()
	cancel()
	select {
	case err := <-ended:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "shutdown did not end the open stream")
	}

	// The ordinary request still finishes with its own context intact
	releaseSlow()
	require.Equal(t, "200 OK done", <-slow)

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 2*time.Second, "shutdown waited for the open stream")
	case <-time.After(15 * time.Second):
		require.FailNow(t, "service did not stop within 15s of shutdown")
	}
}
