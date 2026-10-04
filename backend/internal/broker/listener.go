package broker

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"
)

// defaultSniffTimeout is how long a new connection may take to send its first byte, which tells SOCKS5 from HTTP
const defaultSniffTimeout = 10 * time.Second

// Listener lets the SOCKS5 proxy share the broker port with HTTP, so sandboxes and the relay only ever need that one port
// SOCKS5 connections open with the version byte 5, which no HTTP request starts with, and every other connection goes on to the HTTP server
func (b *Broker) Listener(ln net.Listener) net.Listener {
	m := &muxListener{Listener: ln, serveSOCKS: b.serveSOCKS, revoke: func() {
		if b.deps.Live != nil {
			b.deps.Live.RevokeProxies()
		}
	}, sniffTimeout: b.sniffTimeout, accepted: make(chan accepted), done: make(chan struct{})}
	go m.acceptLoop()
	return m
}

type muxListener struct {
	net.Listener
	serveSOCKS   func(net.Conn)
	sniffTimeout time.Duration
	accepted     chan accepted
	done         chan struct{}
	closeOnce    sync.Once
	revoke       func()
}

// accepted is a connection for the HTTP server, or an error of the underlying listener
type accepted struct {
	conn net.Conn
	err  error
}

// acceptLoop accepts connections and leaves waiting for their first byte to a goroutine each, so a client that stays silent can't hold up the others
func (m *muxListener) acceptLoop() {
	for {
		conn, err := m.Listener.Accept()
		if err != nil {
			// The HTTP server decides whether an error is worth retrying, as it would with the plain listener
			select {
			case m.accepted <- accepted{err: err}:
			case <-m.done:
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go m.route(conn)
	}
}

// route hands a connection to the SOCKS5 proxy or the HTTP server by its first byte
func (m *muxListener) route(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(m.sniffTimeout))
	r := bufio.NewReader(conn)
	first, err := r.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return
	}

	peeked := &peekedConn{Conn: conn, r: r}
	if first[0] == socksVersion {
		m.serveSOCKS(peeked)
		return
	}
	select {
	case m.accepted <- accepted{conn: peeked}:
	case <-m.done:
		_ = conn.Close()
	}
}

func (m *muxListener) Accept() (net.Conn, error) {
	select {
	case a := <-m.accepted:
		return a.conn, a.err
	case <-m.done:
		return nil, net.ErrClosed
	}
}

func (m *muxListener) Close() error {
	m.closeOnce.Do(func() {
		close(m.done)
		if m.revoke != nil {
			m.revoke()
		}
	})
	return m.Listener.Close()
}

// peekedConn reads through the reader that peeked at the first byte, so that byte isn't lost
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}
