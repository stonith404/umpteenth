//go:build unit

package main

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayForwardsAndHalfCloses(t *testing.T) {
	// The target answers with everything it received once the client half-closed
	target, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write(append([]byte("echo:"), data...))
			}()
		}
	}()

	relay, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	go serveRelay(relay, target.Addr().String(), time.Second)

	for range 3 {
		conn, err := net.Dial("tcp", relay.Addr().String())
		require.NoError(t, err)
		_, err = conn.Write([]byte("ping"))
		require.NoError(t, err)
		require.NoError(t, conn.(*net.TCPConn).CloseWrite())
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		reply, err := io.ReadAll(conn)
		require.NoError(t, err)
		assert.Equal(t, "echo:ping", string(reply))
		_ = conn.Close()
	}
}

func TestRelayClosesClientWhenTargetIsDown(t *testing.T) {
	// Reserve a port and close it again so nothing listens there
	unused, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := unused.Addr().String()
	require.NoError(t, unused.Close())

	relay, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	go serveRelay(relay, addr, time.Second)

	conn, err := net.Dial("tcp", relay.Addr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

// sourceListener stands in for the relay's listener on the run networks, where every sandbox connects from its own address
// Loopback gives every test connection the same address, so the listener reports the address of the sandbox that is currently dialing
type sourceListener struct {
	net.Listener
	mu       sync.Mutex
	source   net.IP
	accepted int
}

// sourcedConn reports the sandbox address it was accepted for
type sourcedConn struct {
	net.Conn
	remote net.Addr
}

func (c *sourcedConn) RemoteAddr() net.Addr {
	return c.remote
}

func (l *sourceListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepted++
	port := conn.RemoteAddr().(*net.TCPAddr).Port
	return &sourcedConn{Conn: conn, remote: &net.TCPAddr{IP: l.source, Port: port}}, nil
}

// dialFrom switches the address later connections come from
func (l *sourceListener) dialFrom(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.source = net.ParseIP(ip)
}

func (l *sourceListener) acceptedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepted
}

func TestRelayServesOtherSandboxesWhileOneHoldsEverySlot(t *testing.T) {
	// The target stands in for the broker, which keeps tunnels and busy keep-alive connections open for as long as the sandbox likes
	target, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	front, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := &sourceListener{Listener: front}
	t.Cleanup(func() { _ = ln.Close() })
	go serveRelay(ln, target.Addr().String(), time.Second)

	// Sandbox A opens as many connections as the relay has slots and keeps every one of them open
	ln.dialFrom("172.30.0.2")
	for range maxRelayConns {
		conn, err := net.Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
	}
	require.Eventually(t, func() bool { return ln.acceptedCount() >= maxRelayConns }, 10*time.Second, 10*time.Millisecond)

	// Sandbox B, on another run network, must still reach the broker
	ln.dialFrom("172.31.0.2")
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply := make([]byte, 4)
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err, "sandbox B's connection was not forwarded while sandbox A holds %d relay connections", maxRelayConns)
	assert.Equal(t, "ping", string(reply))

	// Sandbox A's next connection is over its share, so the relay closes it instead of letting it wait for a slot
	ln.dialFrom("172.30.0.2")
	excess, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = excess.Close() }()
	_ = excess.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = excess.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

func TestRelayReleasesAClientTheTargetHungUpOn(t *testing.T) {
	// The target closes every connection right away, like a server timing out a client that sends nothing
	target, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// An idle client must not keep its relay slot once the target is gone
	front, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = front.Close() })
	client, err := net.Dial("tcp", front.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	server, err := front.Accept()
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		relayConn(server, target.Addr().String(), time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the relay kept an idle client after the target closed the connection")
	}
}
