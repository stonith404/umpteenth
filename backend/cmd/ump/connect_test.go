//go:build unit

package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProxy answers one CONNECT request with status, and when it accepts, greets the client and echoes what it sends
func fakeProxy(t *testing.T, status int) (addr string, requests chan *http.Request) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	requests = make(chan *http.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		requests <- req
		if status != http.StatusOK {
			_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 38\r\n\r\nevil.example.org is not on this list\r\n")
			return
		}

		// The greeting goes out together with the answer, like an SSH server that speaks first, and the first line the client sends comes back before the server hangs up
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\nSSH-2.0-fake\n")
		line, _ := reader.ReadString('\n')
		_, _ = io.WriteString(conn, line)
	}()
	return ln.Addr().String(), requests
}

func TestConnectTunnelsStdioThroughTheProxy(t *testing.T) {
	addr, requests := fakeProxy(t, http.StatusOK)
	conn, pending, err := dialProxy("http://"+addr, "tok", "github.com:22")
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	req := <-requests
	assert.Equal(t, http.MethodConnect, req.Method)
	assert.Equal(t, "github.com:22", req.Host)
	user, password, ok := (&http.Request{Header: http.Header{"Authorization": req.Header["Proxy-Authorization"]}}).BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "ump", user)
	assert.Equal(t, "tok", password)

	// Bytes the proxy sent right after its answer arrive first, then the echo of what the client wrote, until the server hangs up
	var out bytes.Buffer
	tunnelStdio(conn, pending, strings.NewReader("hello\n"), &out)
	assert.Equal(t, "SSH-2.0-fake\nhello\n", out.String())
}

func TestConnectReportsWhyTheProxyRefused(t *testing.T) {
	addr, _ := fakeProxy(t, http.StatusForbidden)
	_, _, err := dialProxy("http://"+addr, "tok", "evil.example.org:22")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403 Forbidden")
	assert.Contains(t, err.Error(), "evil.example.org is not on this list")
}

func TestConnectNeedsTheSandboxEnvironment(t *testing.T) {
	t.Setenv("UMP_BROKER_URL", "")
	assert.Equal(t, 2, run([]string{"connect", "github.com"}, io.Discard, io.Discard))
	assert.Equal(t, 1, run([]string{"connect", "github.com", "22"}, io.Discard, io.Discard))
}
