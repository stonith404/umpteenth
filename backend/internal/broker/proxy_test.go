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
	"slices"
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
	registry *runner.Registry
	db       *database.DB
	runID    string
}

// newProxyHarness takes options that adjust the broker before it starts serving, since its connection goroutines read its fields without locks
func newProxyHarness(t *testing.T, network sandbox.NetworkPolicy, domains []string, opts ...func(*Broker)) *proxyHarness {
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

	// The run is live for the broker API, and its sandbox holds a proxy grant like the runner gives it
	registry := runner.NewRegistry()
	live := &runner.LiveRun{
		Run:      runner.Run{ID: runID},
		Job:      runner.JobConfig{Network: network, AllowedDomains: domains},
		Recorder: events.NewRecorder(db, events.NewLocalBus(), storage.NewDatabaseStorage(db), runID),
	}
	registry.Register(runID, live)
	registry.GrantProxy("secret-token", &runner.ProxyGrant{Network: network, AllowedDomains: domains, Live: live})

	b := New(Dependencies{Runs: tokenRuns{crypto.HashToken("secret-token"): runID}, Live: registry})
	b.dialer = func(bool) dialFunc {
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
	}
	for _, opt := range opts {
		opt(b)
	}

	// The broker's own listener lets SOCKS5 share the port, as in production
	server := httptest.NewUnstartedServer(b.Handler())
	server.Listener = b.Listener(server.Listener)
	server.Start()
	t.Cleanup(server.Close)
	return &proxyHarness{server: server, registry: registry, db: db, runID: runID}
}

// addr is the broker's host and port
func (h *proxyHarness) addr() string {
	return strings.TrimPrefix(h.server.URL, "http://")
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
	assert.Contains(t, recorded[1], "not on this job's allow-list")
}

func TestProxyTunnelsConnect(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"*.cdn.example.net"})

	// A CONNECT to an allowed host becomes a tunnel that carries whatever the client speaks, here plain HTTP
	conn, err := net.Dial("tcp", h.addr())
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
	conn2, err := net.Dial("tcp", h.addr())
	require.NoError(t, err)
	defer func() { _ = conn2.Close() }()
	_, err = fmt.Fprintf(conn2, "CONNECT cdn.example.net:443 HTTP/1.1\r\nHost: cdn.example.net:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	require.NoError(t, err)
	resp, err = http.ReadResponse(bufio.NewReader(conn2), nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestProxyLetsInternetSandboxesReachAnyHost(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil)

	// Any name and any IP literal pass the name check, since the dialer checks the addresses they lead to
	for _, target := range []string{"http://api.example.com/a", "http://93.184.216.34/b", "http://[2606:4700::1111]/c"} {
		resp, err := h.client("secret-token").Get(target)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, target)
		assert.Contains(t, string(body), "hello from", target)
	}
}

func TestProxyRefusesSandboxesWithoutProxiedNetwork(t *testing.T) {
	for _, network := range []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkUnrestricted} {
		h := newProxyHarness(t, network, []string{"api.example.com"})
		resp, err := h.client("secret-token").Get("http://api.example.com/")
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, network)
	}
}

func TestProxyServesGrantsWithoutARun(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, nil)

	// Image builds and MCP server tests hold a grant of their own, whose connections no timeline records
	revoke := h.registry.GrantProxy("build-token", &runner.ProxyGrant{Network: sandbox.NetworkInternet})
	resp, err := h.client("build-token").Get("http://deb.debian.org/debian/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, h.proxyEvents(t))

	// A revoked grant no longer opens the proxy
	revoke()
	resp, err = h.client("build-token").Get("http://deb.debian.org/debian/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusProxyAuthRequired, resp.StatusCode)
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

func TestProxyStaysOffContainersAndBlockedRanges(t *testing.T) {
	containers := []netip.Addr{netip.MustParseAddr("172.18.0.5"), netip.MustParseAddr("fd00:18::5")}
	b := New(Dependencies{
		Live:             runner.NewRegistry(),
		Blocked:          []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32"), netip.MustParsePrefix("2001:db8::/32")},
		ContainerAddress: func(addr netip.Addr) bool { return slices.Contains(containers, addr) },
	})

	// Even a job that may reach the private network can't reach a container, a blocked address or cloud metadata, however the address is spelled
	dial := b.dialer(true)
	for _, target := range []string{"172.18.0.5:5432", "[::ffff:172.18.0.5]:5432", "[fd00:18::5%1]:5432", "203.0.113.7:443", "[2001:db8::7%1]:443", "[fd00:ec2::254%1]:80"} {
		_, err := dial(t.Context(), "tcp", target)
		require.ErrorIs(t, err, errRefusedAddress, target)
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
		"0.250.250.254":   {true, false},
		"198.18.0.1":      {true, false},
		"fd00::1":         {true, false},
		"::1":             {true, true},
		"::ffff:10.0.0.1": {true, false},
		"fd00:ec2::254":   {true, true},
		// A zone only picks the interface, so the address behind it is judged
		"fd00:ec2::254%eth0": {true, true},
		"::1%lo":             {true, true},
		// NAT64 addresses are judged by the IPv4 address they reach
		"64:ff9b::a9fe:a9fe": {true, true},
		"64:ff9b::a00:1":     {true, false},
		"64:ff9b::5db8:d822": {false, false},
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
		conn, err := net.Dial("tcp", h.addr())
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

// stalledUpstream never reads what it is sent and stops sending once eof is closed, like a server that half-closes and then ignores the connection
type stalledUpstream struct {
	net.Conn
	eof chan struct{}
}

func (u stalledUpstream) Read([]byte) (int, error) {
	<-u.eof
	return 0, io.EOF
}

func TestRelayEndsWhenEitherSideIsDone(t *testing.T) {
	relayed := func(client, upstream net.Conn) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			relay(client, client, upstream)
			close(done)
		}()
		return done
	}
	requireDone := func(done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("the relay still holds the tunnel open")
		}
	}

	// A sandbox that hangs up ends the tunnel, even though the upstream stays connected and silent
	client, sandboxEnd := net.Pipe()
	upstream, serverEnd := net.Pipe()
	t.Cleanup(func() { _ = serverEnd.Close() })
	done := relayed(client, upstream)
	_ = sandboxEnd.Close()
	requireDone(done)

	// An upstream that stopped sending ends the tunnel, even while a write to it hangs since it never reads
	client, sandboxEnd = net.Pipe()
	upstream, serverEnd = net.Pipe()
	t.Cleanup(func() { _ = sandboxEnd.Close(); _ = serverEnd.Close() })
	eof := make(chan struct{})
	done = relayed(client, stalledUpstream{Conn: upstream, eof: eof})
	_, err := sandboxEnd.Write([]byte("request"))
	require.NoError(t, err)
	close(eof)
	requireDone(done)
}
