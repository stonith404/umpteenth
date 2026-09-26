package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// maxRelayConns caps concurrent connections so a misbehaving sandbox cannot exhaust the relay's file descriptors
const maxRelayConns = 512

func init() {
	register("relay", command{
		Summary:  "Forward TCP connections from internal sandbox networks to the broker",
		Internal: true,
		Run:      runRelay,
	})
}

// runRelay is the entrypoint of the relay container used when Umpteenth runs directly on the host (PLAN.md §4.7.3)
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

		// Block accepting while every slot is busy, which pushes back on the clients
		slots <- struct{}{}
		go func() {
			defer func() { <-slots }()
			relayConn(conn, target, dialTimeout)
		}()
	}
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
