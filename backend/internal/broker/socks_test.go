//go:build unit

package broker

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// socksAuth negotiates SOCKS5 with username and password authentication and returns the connection
// It returns the status of the authentication, which is 0 when the broker accepted the token
func socksAuth(t *testing.T, addr, token string) (net.Conn, byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = conn.Write([]byte{5, 1, 2})
	require.NoError(t, err)
	var choice [2]byte
	_, err = io.ReadFull(conn, choice[:])
	require.NoError(t, err)
	require.Equal(t, [2]byte{5, 2}, choice, "the broker asks for a username and password")

	msg := append([]byte{1, 3}, "ump"...)
	msg = append(msg, byte(len(token))) // #nosec G115 -- test tokens are short
	msg = append(msg, token...)
	_, err = conn.Write(msg)
	require.NoError(t, err)
	var status [2]byte
	_, err = io.ReadFull(conn, status[:])
	require.NoError(t, err)
	return conn, status[1]
}

// socksRequest sends a request for a domain and returns the reply code
func socksRequest(t *testing.T, conn net.Conn, cmd byte, host string, port uint16) byte {
	t.Helper()
	req := append([]byte{5, cmd, 0, 3, byte(len(host))}, host...) // #nosec G115 -- test hosts are short
	req = binary.BigEndian.AppendUint16(req, port)
	_, err := conn.Write(req)
	require.NoError(t, err)
	var reply [10]byte
	_, err = io.ReadFull(conn, reply[:])
	require.NoError(t, err)
	return reply[1]
}

func TestSOCKSRelaysAllowedConnections(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"api.example.com"})
	conn, status := socksAuth(t, h.addr(), "secret-token")
	require.Equal(t, byte(0), status)
	require.Equal(t, byte(socksSucceeded), socksRequest(t, conn, socksConnect, "API.example.com.", 443))

	// The tunnel carries whatever the client speaks, here plain HTTP to the upstream
	_, err := fmt.Fprint(conn, "GET /repo.git HTTP/1.1\r\nHost: api.example.com\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "hello from api.example.com/repo.git", string(body))

	// The connection shows up on the run's timeline like one through the HTTP proxy
	recorded := h.proxyEvents(t)
	require.Len(t, recorded, 1)
	assert.Contains(t, recorded[0], `"proxy api.example.com"`)
}

func TestSOCKSRefusesWhatTheHTTPProxyRefuses(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"api.example.com"})

	// A wrong token fails the authentication
	_, status := socksAuth(t, h.addr(), "wrong")
	assert.Equal(t, byte(1), status)

	// A host off the allow-list is not allowed
	conn, status := socksAuth(t, h.addr(), "secret-token")
	require.Equal(t, byte(0), status)
	assert.Equal(t, byte(socksNotAllowed), socksRequest(t, conn, socksConnect, "evil.example.org", 443))

	// Only CONNECT is supported
	conn, _ = socksAuth(t, h.addr(), "secret-token")
	assert.Equal(t, byte(socksCommandUnsupported), socksRequest(t, conn, 2, "api.example.com", 443))

	// A client that can't authenticate with a username and password is turned away
	plain, err := net.Dial("tcp", h.addr())
	require.NoError(t, err)
	defer func() { _ = plain.Close() }()
	_, err = plain.Write([]byte{5, 1, 0})
	require.NoError(t, err)
	var choice [2]byte
	_, err = io.ReadFull(plain, choice[:])
	require.NoError(t, err)
	assert.Equal(t, [2]byte{5, socksNoMethod}, choice)
}

func TestSOCKSRefusesPrivateAddresses(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil, func(b *Broker) { b.dialer = guardedDialer(nil) })

	// An IPv4 literal on loopback passes the name check but not the dialer
	conn, status := socksAuth(t, h.addr(), "secret-token")
	require.Equal(t, byte(0), status)
	req := []byte{5, socksConnect, 0, socksAddrIPv4, 127, 0, 0, 1, 0, 80}
	_, err := conn.Write(req)
	require.NoError(t, err)
	var reply [10]byte
	_, err = io.ReadFull(conn, reply[:])
	require.NoError(t, err)
	assert.Equal(t, byte(socksNotAllowed), reply[1])
}

func TestSOCKSHandshakeTimesOut(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil, func(b *Broker) { b.socksHandshakeTimeout = 200 * time.Millisecond })

	// A client that announces SOCKS5 and then goes silent is disconnected
	conn, err := net.Dial("tcp", h.addr())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte{5})
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

func TestListenerDoesNotWaitOnSilentClients(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil)

	// Connections that never send a byte don't hold up the ones that do
	for range 5 {
		silent, err := net.Dial("tcp", h.addr())
		require.NoError(t, err)
		defer func() { _ = silent.Close() }()
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(h.server.URL + "/healthz")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestListenerDropsClientsThatNeverSpeak(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkInternet, nil, func(b *Broker) { b.sniffTimeout = 200 * time.Millisecond })

	conn, err := net.Dial("tcp", h.addr())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

func TestSOCKSRepliesAfterASlowDial(t *testing.T) {
	// The upstream takes longer to connect than the negotiation may, as a host whose first addresses drop SYNs does
	h := newProxyHarness(t, sandbox.NetworkInternet, nil, func(b *Broker) {
		b.socksHandshakeTimeout = 300 * time.Millisecond
		upstream := b.dialer
		b.dialer = func(allowPrivate bool) dialFunc {
			return func(ctx context.Context, network, addr string) (net.Conn, error) {
				time.Sleep(600 * time.Millisecond)
				return upstream(allowPrivate)(ctx, network, addr)
			}
		}
	})

	// The client still learns that the tunnel is open and can use it
	conn, status := socksAuth(t, h.addr(), "secret-token")
	require.Equal(t, byte(0), status)
	require.Equal(t, byte(socksSucceeded), socksRequest(t, conn, socksConnect, "api.example.com", 443))
	_, err := fmt.Fprint(conn, "GET /slow HTTP/1.1\r\nHost: api.example.com\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "hello from api.example.com/slow", string(body))
}

func TestSOCKSRepliesAfterASlowFailedDial(t *testing.T) {
	// The dial gives up only after the negotiation deadline, as one toward a blackholed host does
	h := newProxyHarness(t, sandbox.NetworkInternet, nil, func(b *Broker) {
		b.socksHandshakeTimeout = 300 * time.Millisecond
		b.dialer = func(bool) dialFunc {
			return func(context.Context, string, string) (net.Conn, error) {
				time.Sleep(600 * time.Millisecond)
				return nil, errors.New("dial tcp 192.0.2.1:22: i/o timeout")
			}
		}
	})

	// The client still learns why the proxy couldn't connect
	conn, status := socksAuth(t, h.addr(), "secret-token")
	require.Equal(t, byte(0), status)
	assert.Equal(t, byte(socksHostUnreachable), socksRequest(t, conn, socksConnect, "api.example.com", 22))
}
