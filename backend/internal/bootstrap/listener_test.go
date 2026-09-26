//go:build unit

package bootstrap

import (
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilteredListenerRefusesSandboxFacingAddresses(t *testing.T) {
	serve := func(refuse func(netip.Addr) bool) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		filtered := filteredListener{Listener: ln, refuse: refuse}
		t.Cleanup(func() { _ = filtered.Close() })
		go func() {
			for {
				conn, err := filtered.Accept()
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte("hello"))
				_ = conn.Close()
			}
		}()

		conn, err := net.Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, _ := io.ReadAll(conn)
		return string(got)
	}

	// A connection that arrives on a refused address is closed before the server sees it
	assert.Equal(t, "", serve(func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }))
	assert.Equal(t, "hello", serve(func(netip.Addr) bool { return false }))
}
