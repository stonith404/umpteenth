//go:build unit

package main

import (
	"io"
	"net"
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
