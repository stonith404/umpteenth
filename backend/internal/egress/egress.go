// Package egress guards the outbound HTTP calls Umpteenth itself makes, such as to MCP servers and model APIs
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"syscall"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// Guard blocks private and link-local targets unless they are allowed
// Self-hosted instances allow them by default so local Ollama and LAN MCP servers work, and SaaS must not
type Guard struct {
	allowPrivate bool
	// blocked are ranges the operator blocks on top of that, which stay blocked even where private targets are allowed
	blocked []netip.Prefix
	// transport is shared by every client the guard hands out, so they pool connections instead of each leaving its own idle ones behind
	transport *http.Transport
}

func New(allowPrivate bool, blocked ...netip.Prefix) *Guard {
	g := &Guard{allowPrivate: allowPrivate, blocked: blocked}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if g.checks() {
		dialer.Control = g.checkDial
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	g.transport = transport
	return g
}

// checks reports whether the guard can refuse any address at all
func (g *Guard) checks() bool {
	return !g.allowPrivate || len(g.blocked) > 0
}

// refuses reports whether the guard keeps connections away from addr
func (g *Guard) refuses(addr netip.Addr) bool {
	return (!g.allowPrivate && Blocked(addr)) || InRanges(addr, g.blocked)
}

// CheckURL validates a URL before it is stored, resolving its host once
// Field names the request field the URL came from, so the error points at it
func (g *Guard) CheckURL(ctx context.Context, field, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return apperror.InvalidField(field, "invalid", "must be an http(s) URL")
	}
	if !g.checks() {
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(resolveCtx, "ip", u.Hostname())
	if err != nil {
		return apperror.InvalidField(field, "unresolvable", "host cannot be resolved")
	}
	if slices.ContainsFunc(addrs, g.refuses) {
		return apperror.InvalidField(field, "forbidden", "points to a private or local network address")
	}
	return nil
}

// HTTPClient returns a client whose dialer re-checks every resolved address, which also defeats DNS rebinding
func (g *Guard) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: g.transport}
}

// ErrResponseTooLarge is returned when a response grows past a limit, such as one set with LimitResponses or LimitBody
var ErrResponseTooLarge = errors.New("response body too large")

// LimitResponses caps every response body base returns, at errorLimit bytes for a status of 400 or more and at limit bytes otherwise, and reading past the cap fails with ErrResponseTooLarge
// The transport has already inflated a compressed body by then, so the cap counts decompressed bytes and a gzip bomb can't slip past it
// An error body is only ever read for its message, which is why it gets its own, smaller cap
// A nil base stands for http.DefaultTransport
func LimitResponses(base http.RoundTripper, limit, errorLimit int64) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &limitTransport{base: base, limit: limit, errorLimit: errorLimit}
}

type limitTransport struct {
	base       http.RoundTripper
	limit      int64
	errorLimit int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	limit := t.limit
	if resp.StatusCode >= 400 {
		limit = t.errorLimit
	}
	resp.Body = LimitBody(resp.Body, limit)
	return resp, nil
}

// LimitBody caps body at limit bytes, and reading past it fails with ErrResponseTooLarge
func LimitBody(body io.ReadCloser, limit int64) io.ReadCloser {
	return &limitedBody{ReadCloser: body, left: limit}
}

// limitedBody fails once its limit is passed, where io.LimitReader would silently cut the body into something that may still parse
type limitedBody struct {
	io.ReadCloser
	left int64
	err  error
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}

	// Asking for one byte more than is left tells a body that ends right at the limit from one that goes on
	if int64(len(p)) > b.left {
		p = p[:b.left+1]
	}
	n, err := b.ReadCloser.Read(p)
	if int64(n) <= b.left {
		b.left -= int64(n)
		return n, err
	}
	n = int(b.left)
	b.left = 0
	b.err = ErrResponseTooLarge
	return n, b.err
}

// ErrBlocked is returned when the guard refuses a connection, which retrying can't change
var ErrBlocked = errors.New("not allowed")

// checkDial refuses to connect to a refused address, and to anything that isn't an IP address since the dialer only ever passes resolved ones
func (g *Guard) checkDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || g.refuses(addr) {
		return fmt.Errorf("egress to %s: %w", host, ErrBlocked)
	}
	return nil
}

var (
	// sharedAddressSpace is 100.64.0.0/10, where some clouds serve instance metadata, e.g. Alibaba Cloud at 100.100.100.200
	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
	// nat64 addresses reach the IPv4 address in their last 32 bits through a NAT64 gateway
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
	// metadataAddrs are cloud metadata services outside the link-local range: Alibaba Cloud's and the IPv6 one of AWS
	metadataAddrs = []netip.Addr{netip.MustParseAddr("100.100.100.200"), netip.MustParseAddr("fd00:ec2::254")}
	// specialRanges are IPv4 ranges that are neither private nor public, where local services live too, such as OrbStack's host at 0.250.250.254 or the benchmarking range some VPNs hand out
	specialRanges = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4")}
)

// Blocked reports whether a connection to addr could reach the host itself, cloud metadata or a private network
func Blocked(addr netip.Addr) bool {
	addr = reached(addr)
	return hostLocal(addr) || addr.IsPrivate() || sharedAddressSpace.Contains(addr) || slices.ContainsFunc(specialRanges, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// InRanges reports whether a connection to addr ends up in one of the ranges, such as the ones an operator blocks
func InRanges(addr netip.Addr, ranges []netip.Prefix) bool {
	addr = reached(addr)
	return slices.ContainsFunc(ranges, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// HostLocal reports whether a connection to addr could reach the host itself or cloud metadata, which stay off limits even where private networks are allowed
func HostLocal(addr netip.Addr) bool {
	return hostLocal(reached(addr))
}

func hostLocal(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || slices.Contains(metadataAddrs, addr)
}

// Reached is the address a connection to addr really reaches, since IPv4-mapped and NAT64 addresses lead to the IPv4 address they embed
// An IPv6 zone only picks the interface to leave through, so it is dropped, which also keeps range checks working since a prefix never contains a zoned address
func Reached(addr netip.Addr) netip.Addr {
	return reached(addr)
}

func reached(addr netip.Addr) netip.Addr {
	addr = addr.WithZone("").Unmap()
	if nat64.Contains(addr) {
		b := addr.As16()
		addr = netip.AddrFrom4([4]byte(b[12:]))
	}
	return addr
}
