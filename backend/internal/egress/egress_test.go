//go:build unit

package egress

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlockedCoversMetadataAndPrivateRanges(t *testing.T) {
	for addr, want := range map[string]bool{
		"169.254.169.254":    true,
		"100.100.100.200":    true,
		"10.0.0.1":           true,
		"127.0.0.1":          true,
		"::ffff:192.168.1.1": true,
		"64:ff9b::a9fe:a9fe": true,
		"fd00:ec2::254":      true,
		"1.1.1.1":            false,
		"64:ff9b::101:101":   false,
		"2606:4700::1111":    false,
	} {
		assert.Equal(t, want, blocked(netip.MustParseAddr(addr)), addr)
	}
}
