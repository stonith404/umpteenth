package broker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// proxyDialTimeout bounds connecting to an allowed host
const proxyDialTimeout = 15 * time.Second

// hopHeaders apply to one connection only and are never forwarded
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// dialFunc connects the egress proxy to a target
type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// proxy is the egress proxy, the only way out of sandboxes with internet access or an allow-list
// It shares the broker listener with the API and the SOCKS5 proxy, and knows the sandbox by the token it sends as the proxy password
func (b *Broker) proxy(w http.ResponseWriter, r *http.Request) {
	grant, ok := b.proxyGrant(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="umpteenth"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}

	// The grant's network policy decides by name whether the target may be reached at all
	target := r.Host
	if r.Method != http.MethodConnect {
		target = r.URL.Host
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = strings.Trim(target, "[]"), "80"
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if err := checkTarget(grant, host); err != nil {
		b.recordProxy(r.Context(), grant, host, err)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Every connection holds file descriptors on the host, so a sandbox can't open them without bound
	release, ok := grant.AcquireConn()
	if !ok {
		http.Error(w, "the sandbox has too many open proxy connections", http.StatusTooManyRequests)
		return
	}
	defer release()

	dial := b.dialer(grant.AllowPrivateNetwork)
	if r.Method == http.MethodConnect {
		b.tunnel(w, r, grant, host, net.JoinHostPort(host, port), dial)
		return
	}
	b.forward(w, r, grant, host, dial)
}

// proxyGrant resolves the proxy grant from the proxy credentials
func (b *Broker) proxyGrant(r *http.Request) (*runner.ProxyGrant, bool) {
	encoded, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
	if !ok {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, false
	}
	_, token, ok := strings.Cut(string(raw), ":")
	if !ok || token == "" {
		return nil, false
	}
	return b.deps.Live.ProxyGrant(token)
}

// checkTarget decides by name whether a grant may reach a host, before any address is resolved
// Where the name leads is checked again for every address the dialer connects to
func checkTarget(g *runner.ProxyGrant, host string) error {
	switch g.Network {
	case sandbox.NetworkInternet:
		if host == "" {
			return errors.New("the request names no target host")
		}
		return nil
	case sandbox.NetworkAllowlist:
		if !DomainAllowed(host, g.AllowedDomains) {
			return fmt.Errorf("%s is not on this job's allow-list", host)
		}
		return nil
	default:
		return errors.New("the egress proxy only serves sandboxes with internet access or an allow-list")
	}
}

// tunnel connects a CONNECT request to the target and relays bytes both ways until either side closes
func (b *Broker) tunnel(w http.ResponseWriter, r *http.Request, grant *runner.ProxyGrant, host, target string, dial dialFunc) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the proxy cannot tunnel on this connection", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), proxyDialTimeout)
	upstream, err := dial(ctx, "tcp", target)
	cancel()
	b.recordProxy(r.Context(), grant, host, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()

	// The server may have left a deadline on the connection, which must not cut a long-lived tunnel short
	_ = client.SetDeadline(time.Time{})
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	relay(client, buffered, upstream)
}

// relay copies bytes both ways between a client and its upstream until either side is done
// Bytes the client sent before the tunnel was set up are already buffered in from and go first
func relay(client net.Conn, from io.Reader, upstream net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, from)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()

	// Once one direction ends, both connections close, which also ends the other copy, since a peer that half-closes and goes silent or stops reading would otherwise hold it forever
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

// forward sends a plain HTTP request on to the target and copies the response back
func (b *Broker) forward(w http.ResponseWriter, r *http.Request, grant *runner.ProxyGrant, host string, dial dialFunc) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	transport := &http.Transport{DialContext: dial, ResponseHeaderTimeout: time.Minute, DisableKeepAlives: true}
	resp, err := transport.RoundTrip(out)
	b.recordProxy(r.Context(), grant, host, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// recordProxy puts the first connection to each host and every refusal on the run's timeline, within the run's limit of broker calls
// Grants of image builds and MCP server tests belong to no run, so nothing records their connections
func (b *Broker) recordProxy(ctx context.Context, grant *runner.ProxyGrant, host string, err error) {
	if grant.Live == nil || (err == nil && !grant.FirstHost(host)) {
		return
	}
	payload := map[string]any{"endpoint": clip("proxy " + host), "ok": err == nil}
	if err != nil {
		payload["error"] = clip(err.Error())
	}
	emitCall(ctx, grant.Live, events.Event{Type: events.TypeBrokerCall, Payload: payload}, false)
}

// DomainAllowed reports whether a host is on an allow-list: an entry matches its exact name, and *.example.com matches every name below example.com
func DomainAllowed(host string, allowed []string) bool {
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, entry := range allowed {
		entry = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(entry)), ".")
		if suffix, ok := strings.CutPrefix(entry, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

// guardedDialer checks every address it connects to, so a name that resolves to a private address can't reach the private network
// Loopback and metadata addresses are refused even for jobs that may reach the private network, since they lead to Umpteenth itself and the host's credentials
// What offLimits reports is refused for every job
func guardedDialer(offLimits func(netip.Addr) bool) func(allowPrivate bool) dialFunc {
	return func(allowPrivate bool) dialFunc {
		dialer := &net.Dialer{Timeout: proxyDialTimeout, Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if refused(addr, allowPrivate) || (offLimits != nil && offLimits(addr)) {
				return fmt.Errorf("%w: %s", errRefusedAddress, host)
			}
			return nil
		}}
		return dialer.DialContext
	}
}

var errRefusedAddress = errors.New("the egress proxy does not connect to this address")

// refused classifies addresses like the control plane's own egress, so both guards agree on what is private
func refused(addr netip.Addr, allowPrivate bool) bool {
	if allowPrivate {
		return egress.HostLocal(addr)
	}
	return egress.Blocked(addr)
}
