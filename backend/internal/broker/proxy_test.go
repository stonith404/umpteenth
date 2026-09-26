//go:build unit

package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

type tokenRuns map[string]string

func (t tokenRuns) RunIDByBrokerToken(_ context.Context, hash string) (string, error) {
	if id, ok := t[hash]; ok {
		return id, nil
	}
	return "", errors.New("not found")
}

// proxyHarness serves the broker with one live run and sends every proxied connection to a local upstream
type proxyHarness struct {
	server   *httptest.Server
	db       *database.DB
	runID    string
	upstream string
}

func newProxyHarness(t *testing.T, network sandbox.NetworkPolicy, domains []string) *proxyHarness {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'explore', 'manual', 0, $4)`,
		runID, wid, jobID, database.Now())

	// The upstream stands in for the allowed host, since the real dialer never connects to loopback
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// #nosec G705 -- the test upstream echoes the request so the test can see what arrived
		_, _ = fmt.Fprintf(w, "hello from %s%s", r.Host, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	live := runner.NewRegistry()
	live.Register(runID, &runner.LiveRun{
		Run:      runner.Run{ID: runID},
		Job:      runner.JobConfig{Network: network, AllowedDomains: domains},
		Recorder: events.NewRecorder(db, events.NewLocalBus(), storage.NewDatabaseStorage(db), runID),
	})
	b := New(Dependencies{Runs: tokenRuns{crypto.HashToken("secret-token"): runID}, Live: live})
	b.dialer = func(bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
	}
	server := httptest.NewServer(b.Handler())
	t.Cleanup(server.Close)
	return &proxyHarness{server: server, db: db, runID: runID, upstream: upstream.Listener.Addr().String()}
}

// client sends requests through the broker as its proxy, the way HTTP_PROXY makes tools do
func (h *proxyHarness) client(token string) *http.Client {
	proxy, _ := url.Parse(h.server.URL)
	if token != "" {
		proxy.User = url.UserPassword("ump", token)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
}

func (h *proxyHarness) proxyEvents(t *testing.T) []string {
	rows, err := h.db.QueryContext(context.Background(), "SELECT payload FROM run_events WHERE run_id = $1 AND type = 'broker.call' ORDER BY seq", h.runID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		require.NoError(t, rows.Scan(&p))
		out = append(out, p)
	}
	return out
}

func TestProxyForwardsOnlyAllowedDomains(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"api.example.com", "*.cdn.example.net"})

	// An allowed host gets through, and the upstream sees the request as the sandbox sent it
	resp, err := h.client("secret-token").Get("http://api.example.com/v1/items")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "hello from api.example.com/v1/items", string(body))

	// Another host is refused
	resp, err = h.client("secret-token").Get("http://evil.example.org/steal")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Without the run's token the proxy asks for credentials
	resp, err = h.client("").Get("http://api.example.com/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusProxyAuthRequired, resp.StatusCode)
	resp, err = h.client("wrong").Get("http://api.example.com/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusProxyAuthRequired, resp.StatusCode)

	// The timeline shows the first connection to a host and every refusal
	recorded := h.proxyEvents(t)
	require.Len(t, recorded, 2)
	assert.Contains(t, recorded[0], `"proxy api.example.com"`)
	assert.Contains(t, recorded[1], "not on the job's allow-list")
}

func TestProxyTunnelsConnect(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"*.cdn.example.net"})

	// A CONNECT to an allowed host becomes a tunnel that carries whatever the client speaks, here plain HTTP
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.server.URL, "http://"))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	auth := base64.StdEncoding.EncodeToString([]byte("ump:secret-token"))
	_, err = fmt.Fprintf(conn, "CONNECT assets.cdn.example.net:443 HTTP/1.1\r\nHost: assets.cdn.example.net:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	require.NoError(t, err)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = fmt.Fprint(conn, "GET /logo.png HTTP/1.1\r\nHost: assets.cdn.example.net\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	resp, err = http.ReadResponse(reader, nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "hello from assets.cdn.example.net/logo.png", string(body))

	// The bare parent domain is not covered by the wildcard
	conn2, err := net.Dial("tcp", strings.TrimPrefix(h.server.URL, "http://"))
	require.NoError(t, err)
	defer func() { _ = conn2.Close() }()
	_, err = fmt.Fprintf(conn2, "CONNECT cdn.example.net:443 HTTP/1.1\r\nHost: cdn.example.net:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	require.NoError(t, err)
	resp, err = http.ReadResponse(bufio.NewReader(conn2), nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestProxyServesAllowlistJobsOnly(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, []string{"api.example.com"})
	resp, err := h.client("secret-token").Get("http://api.example.com/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestDomainAllowed(t *testing.T) {
	allowed := []string{"api.example.com", "*.example.net", "Mixed.Case.org."}
	for host, want := range map[string]bool{
		"api.example.com":    true,
		"www.example.com":    false,
		"example.net":        false,
		"a.example.net":      true,
		"a.b.example.net":    true,
		"evilexample.net":    false,
		"mixed.case.org":     true,
		"10.0.0.1":           false,
		"::1":                false,
		"":                   false,
		"api.example.com.ev": false,
	} {
		assert.Equal(t, want, DomainAllowed(host, allowed), host)
	}
}

func TestRefusedAddresses(t *testing.T) {
	for addr, want := range map[string][2]bool{
		// address: refused without, refused with private access
		"93.184.216.34":   {false, false},
		"10.1.2.3":        {true, false},
		"192.168.1.10":    {true, false},
		"100.64.0.1":      {true, false},
		"127.0.0.1":       {true, true},
		"169.254.169.254": {true, true},
		"100.100.100.200": {true, true},
		"0.0.0.0":         {true, true},
		"fd00::1":         {true, false},
		"::1":             {true, true},
	} {
		a := netip.MustParseAddr(addr)
		assert.Equal(t, want[0], refused(a, false), "%s without private access", addr)
		assert.Equal(t, want[1], refused(a, true), "%s with private access", addr)
	}
}

func TestBrokerCallsRecordBoundedEvents(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil)

	// A megabyte of tool name comes back in the error, which must not be stored in full
	body, err := json.Marshal(map[string]any{"server": "s", "tool": strings.Repeat("A", 1<<20)})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1/mcp/call", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	var stored int64
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT COALESCE(SUM(LENGTH(payload)), 0) FROM run_events WHERE run_id = $1", h.runID).Scan(&stored))
	var blobs int64
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT COALESCE(SUM(size), 0) FROM blobs").Scan(&blobs))
	require.Less(t, stored+blobs, int64(16<<10))
}

func TestProxyTunnelEndsWhenTheUpstreamCloses(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"*.cdn.example.net"})
	auth := base64.StdEncoding.EncodeToString([]byte("ump:secret-token"))
	before := runtime.NumGoroutine()

	// Each tunnel carries one request the upstream answers and closes after, while the client stays connected and silent
	for range 20 {
		conn, err := net.Dial("tcp", strings.TrimPrefix(h.server.URL, "http://"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		_, err = fmt.Fprintf(conn, "CONNECT a.cdn.example.net:443 HTTP/1.1\r\nHost: a.cdn.example.net:443\r\nProxy-Authorization: Basic %s\r\n\r\nGET / HTTP/1.1\r\nHost: a.cdn.example.net\r\nConnection: close\r\n\r\n", auth)
		require.NoError(t, err)
		_, _ = io.ReadAll(conn)
	}

	// The proxy lets go of a tunnel whose upstream is gone instead of waiting on the client forever
	require.Eventually(t, func() bool { return runtime.NumGoroutine() < before+10 }, 3*time.Second, 50*time.Millisecond,
		"goroutines went from %d to %d", before, runtime.NumGoroutine())
}
