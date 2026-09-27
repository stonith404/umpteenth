package docker

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/docker/docker/api/types/network"
)

// containerRangesTTL bounds how long the engine's network ranges are trusted, since other tools create networks too
const containerRangesTTL = 30 * time.Second

// containerRanges caches the address ranges of the engine's networks, which the egress proxy checks every connection against
type containerRanges struct {
	mu     sync.Mutex
	loaded time.Time
	// ok is set once the engine answered, so an engine that never did keeps every non-public address off limits
	ok bool
	// subnets are the ranges containers get addresses from, and gateways are the host's own addresses in them
	subnets  []netip.Prefix
	gateways []netip.Addr
}

// ContainerAddress reports whether an address belongs to a container on the engine rather than to the host, such as another run's sandbox or a database next to Umpteenth
// The egress proxy dials from this process, which can reach every container network of the engine, so it refuses these even for jobs that may reach the private network
func (a *Adapter) ContainerAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	r := &a.containers
	r.mu.Lock()
	defer r.mu.Unlock()

	// A stale list is reloaded, and one that can't be is kept, since networks rarely go away
	if time.Since(r.loaded) > containerRangesTTL {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		subnets, gateways, err := a.listContainerRanges(ctx)
		cancel()
		if err == nil {
			r.subnets, r.gateways, r.ok = subnets, gateways, true
			r.loaded = time.Now()
		} else {
			a.log.Warn("Failed to list the engine's networks for the egress proxy", "error", err)
		}
	}

	// Without an answer from the engine every non-public address could be a container's
	if !r.ok {
		return addr.IsPrivate() || !addr.IsGlobalUnicast()
	}
	return inContainerRange(addr, r.subnets, r.gateways)
}

// forgetContainerRanges makes the next check reload the ranges, after this adapter created a network whose range the list lacks
func (a *Adapter) forgetContainerRanges() {
	a.containers.mu.Lock()
	a.containers.loaded = time.Time{}
	a.containers.mu.Unlock()
}

// listContainerRanges returns the subnets of every network on the engine and the gateway addresses the host holds in them
func (a *Adapter) listContainerRanges(ctx context.Context) ([]netip.Prefix, []netip.Addr, error) {
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	var subnets []netip.Prefix
	var gateways []netip.Addr
	for _, n := range networks {
		for _, c := range n.IPAM.Config {
			if subnet, err := netip.ParsePrefix(c.Subnet); err == nil {
				subnets = append(subnets, subnet.Masked())
			}
			if gateway, err := netip.ParseAddr(c.Gateway); err == nil {
				gateways = append(gateways, gateway.Unmap())
			}
		}
	}
	return subnets, gateways, nil
}

// inContainerRange reports whether addr lies in one of the subnets without being one of the gateways, which lead to the host
func inContainerRange(addr netip.Addr, subnets []netip.Prefix, gateways []netip.Addr) bool {
	for _, gateway := range gateways {
		if gateway == addr {
			return false
		}
	}
	for _, subnet := range subnets {
		if subnet.Contains(addr) {
			return true
		}
	}
	return false
}
