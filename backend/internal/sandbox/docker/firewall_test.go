//go:build unit

package docker

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFirewallScript(t *testing.T) {
	script := firewallScript("br-strict", "br-private")

	// The strict bridge may reach neither private ranges nor the host, the private one only loses metadata
	assert.Contains(t, script, "jump DOCKER-USER -i br-strict -j UMPTEENTH-PRIVATE")
	assert.Contains(t, script, "jump INPUT -i br-strict -j UMPTEENTH-HOST")
	assert.Contains(t, script, "jump DOCKER-USER -i br-private -j UMPTEENTH-METADATA")
	for _, r := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
		assert.Contains(t, script, "add UMPTEENTH-PRIVATE -d "+r+" -j REJECT")
	}
	assert.Contains(t, script, "add UMPTEENTH-METADATA -d 169.254.0.0/16 -j REJECT")
	assert.NotContains(t, script, "add UMPTEENTH-METADATA -d 10.0.0.0/8")

	// Replies come first, and nothing is flushed, so chains shared with another installation never go empty
	assert.Less(t, strings.Index(script, "add UMPTEENTH-PRIVATE -m conntrack"), strings.Index(script, "add UMPTEENTH-PRIVATE -d"))
	assert.NotContains(t, script, " -F ")
}

func TestSandboxFacingAddresses(t *testing.T) {
	a := &Adapter{}
	a.markSandboxFacing("ump-run-1", "172.30.0.2/16")
	a.markSandboxFacing("ump-run-2", "not an address")
	assert.True(t, a.SandboxFacing(netip.MustParseAddr("172.30.0.2")))
	assert.False(t, a.SandboxFacing(netip.MustParseAddr("172.30.0.3")))
	assert.False(t, a.SandboxFacing(netip.MustParseAddr("127.0.0.1")))
}
