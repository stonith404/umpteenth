package broker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SOCKS5 lets tools that need plain TCP, such as ssh or database clients, leave the sandbox through the egress proxy (RFC 1928)
const (
	socksVersion = 5
	// socksAuthVersion is the version of the username and password subnegotiation (RFC 1929)
	socksAuthVersion = 1
	socksMethodAuth  = 2
	socksNoMethod    = 0xff
	socksConnect     = 1

	socksAddrIPv4   = 1
	socksAddrDomain = 3
	socksAddrIPv6   = 4

	socksSucceeded          = 0
	socksGeneralFailure     = 1
	socksNotAllowed         = 2
	socksHostUnreachable    = 4
	socksConnectionRefused  = 5
	socksCommandUnsupported = 7
	socksAddressUnsupported = 8
)

// defaultSOCKSHandshakeTimeout bounds the negotiation, so a client that goes silent can't hold its connection open
const defaultSOCKSHandshakeTimeout = 10 * time.Second

// serveSOCKS speaks SOCKS5 on a connection whose first byte announced it
// It supports CONNECT only, and the client authenticates with a username and password whose password is the broker token
func (b *Broker) serveSOCKS(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(b.socksHandshakeTimeout))

	// The client offers its authentication methods, and only username and password carries the token
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil || head[0] != socksVersion {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if !slices.Contains(methods, socksMethodAuth) {
		_, _ = conn.Write([]byte{socksVersion, socksNoMethod})
		return
	}
	if _, err := conn.Write([]byte{socksVersion, socksMethodAuth}); err != nil {
		return
	}

	// The password is the broker token, which names the proxy grant, and the username is ignored
	var ver [1]byte
	if _, err := io.ReadFull(conn, ver[:]); err != nil || ver[0] != socksAuthVersion {
		return
	}
	if _, err := readSOCKSString(conn); err != nil {
		return
	}
	token, err := readSOCKSString(conn)
	if err != nil {
		return
	}
	grant, ok := b.deps.Live.ProxyGrant(token)
	if !ok {
		_, _ = conn.Write([]byte{socksAuthVersion, 1})
		return
	}
	if _, err := conn.Write([]byte{socksAuthVersion, 0}); err != nil {
		return
	}

	// The request names the target, which has to pass the same checks as one through the HTTP proxy
	host, port, reply := readSOCKSRequest(conn)
	if reply != socksSucceeded {
		writeSOCKSReply(conn, reply)
		return
	}
	ctx := grant.Context()
	untrack := grant.TrackConn(conn)
	defer untrack()
	if err := checkTarget(grant, host); err != nil {
		b.recordProxy(ctx, grant, host, err)
		writeSOCKSReply(conn, socksNotAllowed)
		return
	}
	release, ok := grant.AcquireConn()
	if !ok {
		writeSOCKSReply(conn, socksGeneralFailure)
		return
	}
	defer release()

	// The dial may take longer than the handshake deadline allows, which would cut off the reply that follows it
	// The dial has its own timeout, and the tunnel after it lives as long as both sides keep it open
	_ = conn.SetDeadline(time.Time{})
	dialCtx, cancel := context.WithTimeout(ctx, proxyDialTimeout)
	upstream, err := b.grantDialer(grant)(dialCtx, "tcp", net.JoinHostPort(host, port))
	cancel()
	b.recordProxy(ctx, grant, host, err)
	if err != nil {
		writeSOCKSReply(conn, socksDialReply(err))
		return
	}
	defer func() { _ = upstream.Close() }()

	writeSOCKSReply(conn, socksSucceeded)
	relay(conn, conn, upstream)
}

// readSOCKSString reads a string that starts with its length in one byte
func readSOCKSString(r io.Reader) (string, error) {
	var n [1]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return "", err
	}
	s := make([]byte, n[0])
	if _, err := io.ReadFull(r, s); err != nil {
		return "", err
	}
	return string(s), nil
}

// readSOCKSRequest reads a CONNECT request and returns its target, or the reply that refuses it
func readSOCKSRequest(r io.Reader) (host, port string, reply byte) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil || head[0] != socksVersion {
		return "", "", socksGeneralFailure
	}
	if head[1] != socksConnect {
		return "", "", socksCommandUnsupported
	}

	switch head[3] {
	case socksAddrIPv4, socksAddrIPv6:
		raw := make([]byte, 4)
		if head[3] == socksAddrIPv6 {
			raw = make([]byte, 16)
		}
		if _, err := io.ReadFull(r, raw); err != nil {
			return "", "", socksGeneralFailure
		}
		addr, _ := netip.AddrFromSlice(raw)
		host = addr.Unmap().String()
	case socksAddrDomain:
		name, err := readSOCKSString(r)
		if err != nil {
			return "", "", socksGeneralFailure
		}
		if !asciiHost(name) {
			return "", "", socksNotAllowed
		}
		host = strings.TrimSuffix(strings.ToLower(name), ".")
	default:
		return "", "", socksAddressUnsupported
	}

	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", "", socksGeneralFailure
	}
	return host, strconv.Itoa(int(p[0])<<8 | int(p[1])), socksSucceeded
}

// writeSOCKSReply answers a request, always with an unspecified bound address since clients don't use it
func writeSOCKSReply(w io.Writer, reply byte) {
	_, _ = w.Write([]byte{socksVersion, reply, 0, socksAddrIPv4, 0, 0, 0, 0, 0, 0})
}

// socksDialReply tells a client why the proxy couldn't connect
func socksDialReply(err error) byte {
	switch {
	case errors.Is(err, errRefusedAddress):
		return socksNotAllowed
	case errors.Is(err, syscall.ECONNREFUSED):
		return socksConnectionRefused
	default:
		return socksHostUnreachable
	}
}
