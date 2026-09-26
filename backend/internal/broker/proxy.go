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
	"sync"
	"syscall"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// proxyDialTimeout bounds connecting to an allowed host
const proxyDialTimeout = 15 * time.Second

// hopHeaders apply to one connection only and are never forwarded
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// proxy is the egress proxy of allow-list sandboxes, which reach nothing but the domains their job lists (PLAN.md §4.7.3)
// It shares the broker listener and knows the run by the token sandboxes send as the proxy password
func (b *Broker) proxy(w http.ResponseWriter, r *http.Request) {
	live, ok := b.proxyRun(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="umpteenth"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if live.Job.Network != sandbox.NetworkAllowlist {
		http.Error(w, "the egress proxy only serves jobs with an allow-list", http.StatusForbidden)
		return
	}

	// The target host must be on the job's allow-list, and IP addresses are never on it
	target := r.Host
	if r.Method != http.MethodConnect {
		target = r.URL.Host
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, "80"
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !DomainAllowed(host, live.Job.AllowedDomains) {
		b.recordProxy(r.Context(), live, host, fmt.Errorf("%s is not on the job's allow-list", host))
		http.Error(w, fmt.Sprintf("%s is not on this job's allow-list", host), http.StatusForbidden)
		return
	}

	dial := b.dialer(live.Job.AllowPrivateNetwork)
	if r.Method == http.MethodConnect {
		b.tunnel(w, r, live, host, net.JoinHostPort(host, port), dial)
		return
	}
	b.forward(w, r, live, host, dial)
}

// proxyRun resolves the run from the proxy credentials
func (b *Broker) proxyRun(r *http.Request) (*runner.LiveRun, bool) {
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
	live, err := b.liveRun(r.Context(), token)
	return live, err == nil
}

// tunnel connects a CONNECT request to the target and relays bytes both ways until either side closes
func (b *Broker) tunnel(w http.ResponseWriter, r *http.Request, live *runner.LiveRun, host, target string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	ctx, cancel := context.WithTimeout(r.Context(), proxyDialTimeout)
	upstream, err := dial(ctx, "tcp", target)
	cancel()
	b.recordProxy(r.Context(), live, host, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the proxy cannot tunnel on this connection", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// Bytes the client sent after the CONNECT request are already buffered and go first
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, buffered)
		closeWrite(upstream)
	}()
	_, _ = io.Copy(client, upstream)

	// The upstream is done, so the client side is closed too, which also ends the copy above instead of it waiting on a client that may never speak again
	_ = client.Close()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if tcp, ok := c.(interface{ CloseWrite() error }); ok {
		_ = tcp.CloseWrite()
	}
}

// forward sends a plain HTTP request on to the target and copies the response back
func (b *Broker) forward(w http.ResponseWriter, r *http.Request, live *runner.LiveRun, host string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	transport := &http.Transport{DialContext: dial, ResponseHeaderTimeout: time.Minute, DisableKeepAlives: true}
	resp, err := transport.RoundTrip(out)
	b.recordProxy(r.Context(), live, host, err)
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

// recordProxy puts the first connection to each host and every refusal on the run's timeline
func (b *Broker) recordProxy(ctx context.Context, live *runner.LiveRun, host string, err error) {
	if err == nil && !live.FirstProxyHost(host) {
		return
	}
	payload := map[string]any{"endpoint": clip("proxy " + host), "ok": err == nil}
	if err != nil {
		payload["error"] = clip(err.Error())
	}
	live.Recorder.Emit(ctx, events.Event{Type: events.TypeBrokerCall, Payload: payload})
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

// guardedDialer checks every address it connects to, so an allowed name that resolves to a private address can't reach the private network
// Loopback and metadata addresses are refused even for jobs that may reach the private network, since they lead to Umpteenth itself and the host's credentials
func guardedDialer(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: proxyDialTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		if refused(addr.Unmap(), allowPrivate) {
			return fmt.Errorf("%w: %s", errRefusedAddress, host)
		}
		return nil
	}}
	return dialer.DialContext
}

var errRefusedAddress = errors.New("the egress proxy does not connect to this address")

// metadata is the cloud metadata address outside the link-local range
var metadata = netip.MustParseAddr("100.100.100.200")

func refused(addr netip.Addr, allowPrivate bool) bool {
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr == metadata {
		return true
	}
	return !allowPrivate && (addr.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr))
}
