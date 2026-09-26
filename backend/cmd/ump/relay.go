package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// maxRelayConns caps concurrent connections so a misbehaving sandbox cannot exhaust the relay's file descriptors
const maxRelayConns = 512

// maxRelayConnsPerSource is the share of those connections one sandbox may hold, since every run of the replica reaches the broker through this relay
// It covers the 256 egress tunnels the broker allows a sandbox plus room for its ump calls, and still leaves other sandboxes enough slots when one opens all it may
const maxRelayConnsPerSource = 320

func init() {
	register("relay", command{
		Summary:  "Forward TCP connections from internal sandbox networks to the broker",
		Internal: true,
		Run:      runRelay,
	})
}

// runRelay is the entrypoint of the relay container used when Umpteenth runs directly on the host
// It only forwards the broker port, so it can never act as a router between runs
func runRelay(args []string) int {
	flags := flag.NewFlagSet("ump relay", flag.ContinueOnError)
	listen := flags.String("listen", ":8081", "address to accept connections on")
	target := flags.String("target", "", "host:port to forward every connection to")
	dialTimeout := flags.Duration("dial-timeout", 10*time.Second, "timeout for connecting to the target")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *target == "" {
		errorf("relay", "--target is required")
		return 2
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		errorf("relay", "%v", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stderr, "ump relay: forwarding %s to %s\n", ln.Addr(), *target)
	serveRelay(ln, *target, *dialTimeout)
	return 0
}

// serveRelay accepts connections until the listener is closed and pipes each one to target
func serveRelay(ln net.Listener, target string, dialTimeout time.Duration) {
	slots := make(chan struct{}, maxRelayConns)
	shares := relayShares{conns: map[string]int{}}
	for {
		conn, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			// Transient errors such as running out of file descriptors must not end the relay
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// A sandbox over its share is turned away instead of blocking the accept loop every other sandbox depends on
		source := relaySource(conn)
		if !shares.acquire(source) {
			_ = conn.Close()
			continue
		}

		// Block accepting while every slot is busy, which pushes back on the clients
		slots <- struct{}{}
		go func() {
			defer func() {
				<-slots
				shares.release(source)
			}()
			relayConn(conn, target, dialTimeout)
		}()
	}
}

// relayShares counts the connections each source address holds, so no single sandbox can take every slot of the relay
type relayShares struct {
	mu    sync.Mutex
	conns map[string]int
}

// acquire reserves a connection for source, returning false when it already holds its share
func (s *relayShares) acquire(source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[source] >= maxRelayConnsPerSource {
		return false
	}
	s.conns[source]++
	return true
}

// release frees a connection of source and forgets a source once it holds none, so the map only tracks sandboxes that are connected
func (s *relayShares) release(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[source]--
	if s.conns[source] <= 0 {
		delete(s.conns, source)
	}
}

// relaySource identifies the sandbox behind a connection by its address, since every sandbox sits alone on a run network of its own
func relaySource(conn net.Conn) string {
	addr := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// relayConn connects one client to the target and copies both directions until both are done
func relayConn(client net.Conn, target string, dialTimeout time.Duration) {
	defer func() { _ = client.Close() }()
	upstream, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		errorf("relay", "failed to connect to %s: %v", target, err)
		return
	}
	defer func() { _ = upstream.Close() }()

	done := make(chan struct{}, 2)
	go func() {
		pipeHalf(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		pipeHalf(client, upstream)

		// The target is done with the connection, so a client that keeps its side open would only hold a relay slot
		_ = client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

// pipeHalf copies src to dst and propagates the end of the stream
// A clean EOF becomes a half-close so request/response protocols can finish, while an error tears down both sides
func pipeHalf(dst, src net.Conn) {
	_, err := io.Copy(dst, src)
	if err != nil {
		_ = dst.Close()
		_ = src.Close()
		return
	}
	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
		return
	}
	_ = dst.Close()
}
