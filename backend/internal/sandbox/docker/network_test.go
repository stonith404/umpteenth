//go:build unit

package docker

import (
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
)

func TestInContainerRangeLeavesGatewaysToTheHost(t *testing.T) {
	subnets := []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16"), netip.MustParsePrefix("192.168.97.0/24")}
	gateways := []netip.Addr{netip.MustParseAddr("172.17.0.1"), netip.MustParseAddr("192.168.97.1")}
	assert.True(t, inContainerRange(netip.MustParseAddr("172.17.0.2"), subnets, gateways))
	assert.True(t, inContainerRange(netip.MustParseAddr("192.168.97.14"), subnets, gateways))
	assert.False(t, inContainerRange(netip.MustParseAddr("172.17.0.1"), subnets, gateways), "the gateway is the host")
	assert.False(t, inContainerRange(netip.MustParseAddr("192.168.1.20"), subnets, gateways), "the LAN is no container network")
}

func TestContainerAddressFailsClosedWithoutTheEngine(t *testing.T) {
	a := &Adapter{log: slog.New(slog.DiscardHandler)}
	a.containers.loaded = time.Now()

	// Until the engine answered once, every non-public address could be a container's
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("172.17.0.2")))
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("10.1.2.3")))
	assert.False(t, a.ContainerAddress(netip.MustParseAddr("93.184.216.34")))
}

func TestSandboxFacingAddresses(t *testing.T) {
	a := &Adapter{}
	a.markSandboxFacing("ump-run-1", netip.MustParseAddr("172.30.0.2"))
	assert.True(t, a.SandboxFacing(netip.MustParseAddr("172.30.0.2")))
	assert.False(t, a.SandboxFacing(netip.MustParseAddr("172.30.0.3")))
	assert.False(t, a.SandboxFacing(netip.MustParseAddr("127.0.0.1")))
}

func TestNetworkOptionsKeepTheHostOffRunNetworks(t *testing.T) {
	assert.Equal(t, map[string]string{"com.docker.network.bridge.inhibit_ipv4": "true"}, networkOptions())
}

func TestReusableNetworkRequiresTheSameRun(t *testing.T) {
	labels := map[string]string{labelInstance: "inst", labelRun: "run-1"}
	existing := func(labels map[string]string) network.Inspect {
		return network.Inspect{Labels: labels}
	}

	assert.True(t, reusableNetwork(existing(labels), labels))

	// Networks of another run or installation are never taken over
	assert.False(t, reusableNetwork(existing(map[string]string{labelInstance: "inst", labelRun: "run-2"}), labels))
	assert.False(t, reusableNetwork(existing(map[string]string{labelInstance: "other", labelRun: "run-1"}), labels))
}
