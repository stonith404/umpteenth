package main

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// connectDialTimeout bounds reaching the broker and the proxy's answer, which includes connecting to the target
const connectDialTimeout = 30 * time.Second

func init() {
	register("connect", command{
		Summary: "Open a TCP connection through the egress proxy on stdin and stdout, e.g. as an SSH ProxyCommand: ump connect <host> <port>",
		Run:     runConnect,
	})
}

// runConnect lets tools that take a command for their connection, such as ssh with ProxyCommand, leave the sandbox through the egress proxy
func runConnect(args []string) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		errorf("connect", "usage: ump connect <host> <port>")
		return 2
	}
	conn, pending, err := dialProxy(os.Getenv("UMP_BROKER_URL"), os.Getenv("UMP_TOKEN"), net.JoinHostPort(args[0], args[1]))
	if err != nil {
		errorf("connect", "%v", err)
		return 1
	}
	defer func() { _ = conn.Close() }()
	tunnelStdio(conn, pending, os.Stdin, os.Stdout)
	return 0
}

// dialProxy opens a CONNECT tunnel to target through the egress proxy on the broker
// It returns the connection to write to and the reader to read from, which holds anything the proxy sent right after its answer
func dialProxy(brokerURL, token, target string) (net.Conn, io.Reader, error) {
	u, err := url.Parse(strings.TrimRight(brokerURL, "/"))
	if err != nil || u.Host == "" || token == "" {
		return nil, nil, errors.New("UMP_BROKER_URL and UMP_TOKEN are not set; ump only works inside an Umpteenth sandbox")
	}
	conn, err := net.DialTimeout("tcp", u.Host, connectDialTimeout) // #nosec G704 -- the broker URL is set by the adapter in the sandbox environment, and reaching it is the point of this command
	if err != nil {
		return nil, nil, fmt.Errorf("failed to reach the broker: %w", err)
	}

	// The proxy knows the sandbox by the token it sends as the proxy password
	_ = conn.SetDeadline(time.Now().Add(connectDialTimeout))
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: http.Header{"Proxy-Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("ump:"+token))}},
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("failed to send the request to the egress proxy: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req) //nolint:bodyclose // a 200 answer to CONNECT has no body of its own, and closing it would drain the tunnel
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("failed to read the egress proxy's answer: %w", err)
	}

	// The proxy explains a refusal in the body, such as a host that is not on the job's allow-list
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = conn.Close()
		return nil, nil, fmt.Errorf("the egress proxy refused %s: %s", target, strings.TrimSpace(resp.Status+": "+string(body)))
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, nil
}

// tunnelStdio copies in to the tunnel and the tunnel to out until the far end closes
// The end of in leaves the tunnel open, like nc without -N, since some servers drop a client that half-closes before they answered
func tunnelStdio(conn net.Conn, from io.Reader, in io.Reader, out io.Writer) {
	go func() { _, _ = io.Copy(conn, in) }()
	_, _ = io.Copy(out, from)
}
