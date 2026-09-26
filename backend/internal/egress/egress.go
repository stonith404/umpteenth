// Package egress guards the outbound HTTP calls Umpteenth itself makes, such as to MCP servers and model APIs (PLAN.md §3.5)
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// Guard blocks private and link-local targets unless they are allowed
// Self-hosted instances allow them by default so local Ollama and LAN MCP servers work, and SaaS must not
type Guard struct {
	allowPrivate bool
}

func New(allowPrivate bool) *Guard {
	return &Guard{allowPrivate: allowPrivate}
}

// AllowsPrivate reports whether private and local targets are allowed, so clients can say so before a save fails
func (g *Guard) AllowsPrivate() bool {
	return g.allowPrivate
}

// CheckURL validates a URL before it is stored, resolving its host once
// Field names the request field the URL came from, so the error points at it
func (g *Guard) CheckURL(ctx context.Context, field, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return apperror.InvalidField(field, "invalid", "must be an http(s) URL")
	}
	if g.allowPrivate {
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(resolveCtx, "ip", u.Hostname())
	if err != nil {
		return apperror.InvalidField(field, "unresolvable", "host cannot be resolved")
	}
	for _, addr := range addrs {
		if blocked(addr) {
			return apperror.InvalidField(field, "forbidden", "points to a private or local network address")
		}
	}
	return nil
}

// HTTPClient returns a client whose dialer re-checks every resolved address, which also defeats DNS rebinding
func (g *Guard) HTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !g.allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err == nil && blocked(addr) {
				return fmt.Errorf("egress to %s is not allowed", host)
			}
			return nil
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{Timeout: timeout, Transport: transport}
}

var (
	// sharedAddressSpace is 100.64.0.0/10, where some clouds serve instance metadata, e.g. Alibaba Cloud at 100.100.100.200
	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
	// nat64 addresses reach the IPv4 address in their last 32 bits through a NAT64 gateway
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

func blocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if nat64.Contains(addr) {
		b := addr.As16()
		addr = netip.AddrFrom4([4]byte(b[12:]))
	}
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() || addr.IsMulticast() || addr.IsInterfaceLocalMulticast() || sharedAddressSpace.Contains(addr)
}
