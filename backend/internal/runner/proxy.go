package runner

import (
	"context"
	"net"
	"sync"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// maxProxyConns bounds the egress proxy connections a sandbox holds open at once, since a tunnel lasts as long as the sandbox keeps it open
const maxProxyConns = 256

// ProxyGrant lets a sandbox or an image build use the egress proxy on the broker listener, under the network policy it was created with
// Runs, shadow runs, MCP server tests and image builds each hold one for as long as their sandbox or build lives
type ProxyGrant struct {
	Network             sandbox.NetworkPolicy
	AllowedDomains      []string
	AllowPrivateNetwork bool
	// Live is the run whose timeline shows the proxied connections, nil when nothing records them
	Live *LiveRun

	mu sync.Mutex
	// hosts are the hosts the sandbox connected to through the egress proxy
	hosts map[string]bool
	// conns counts the egress proxy connections the sandbox holds open
	conns  int
	closed bool
	active map[net.Conn]struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// runGrant is the proxy grant of a run's sandbox, which follows the job's network settings
func runGrant(live *LiveRun) *ProxyGrant {
	return &ProxyGrant{Network: live.Job.Network, AllowedDomains: live.Job.AllowedDomains, AllowPrivateNetwork: live.Job.AllowPrivateNetwork, Live: live}
}

// FirstHost reports whether the egress proxy connects to a host for the first time under this grant, so the timeline shows each host once
// The timeline takes no more broker calls than the run's limit, so the grant remembers one host past it at most, whose call still notes that later ones are left out
// A sandbox making up names without end would otherwise fill memory with them
func (g *ProxyGrant) FirstHost(host string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hosts[host] || len(g.hosts) > MaxBrokerEvents {
		return false
	}
	if g.hosts == nil {
		g.hosts = map[string]bool{}
	}
	g.hosts[host] = true
	return true
}

// AcquireConn reserves one of the grant's egress proxy connections, returning false when it holds too many already
func (g *ProxyGrant) AcquireConn() (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.conns >= maxProxyConns {
		return nil, false
	}
	g.conns++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.conns--
	}, true
}

// GrantProxy lets the holder of a broker token use the egress proxy until revoke is called
// Grants are keyed by the token's hash, like shadow runs, since only this replica's broker is wired to the sandbox
func (r *Registry) GrantProxy(token string, g *ProxyGrant) (revoke func()) {
	hash := crypto.HashToken(token)
	r.mu.Lock()
	r.grants[hash] = g
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.grants, hash)
		r.mu.Unlock()
		g.revoke()
	}
}

// ProxyGrant returns the grant a broker token holds
func (r *Registry) ProxyGrant(token string) (*ProxyGrant, bool) {
	hash := crypto.HashToken(token)
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.grants[hash]
	return g, ok
}

// Context is canceled on revocation, including while a proxy connection is still being established
func (g *ProxyGrant) Context() context.Context {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx == nil {
		g.ctx, g.cancel = context.WithCancel(context.Background())
		if g.closed {
			g.cancel()
		}
	}
	return g.ctx
}

// TrackConn closes a connection on revocation even if both relay directions are stalled
func (g *ProxyGrant) TrackConn(conn net.Conn) (untrack func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		_ = conn.Close()
		return func() {}
	}
	if g.active == nil {
		g.active = map[net.Conn]struct{}{}
	}
	g.active[conn] = struct{}{}
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.active, conn)
	}
}

func (g *ProxyGrant) revoke() {
	// Detach the sockets under the lock and close them outside it so blocked I/O cannot delay admission checks
	g.mu.Lock()
	g.closed = true
	if g.cancel != nil {
		g.cancel()
	}
	active := g.active
	g.active = nil
	g.mu.Unlock()
	for conn := range active {
		_ = conn.Close()
	}
}

// RevokeProxies closes active proxy connections during broker shutdown
func (r *Registry) RevokeProxies() {
	r.mu.Lock()
	grants := r.grants
	r.grants = map[string]*ProxyGrant{}
	r.mu.Unlock()
	for _, grant := range grants {
		grant.revoke()
	}
}
