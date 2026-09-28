//go:build unit

package egress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"0.250.250.254":      true,
		"198.18.0.1":         true,
		"192.0.0.8":          true,
		"255.255.255.255":    true,
		"1.1.1.1":            false,
		"64:ff9b::101:101":   false,
		"2606:4700::1111":    false,
	} {
		assert.Equal(t, want, Blocked(netip.MustParseAddr(addr)), addr)
	}
}

func TestHostLocalLeavesPrivateNetworksReachable(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":          true,
		"::1":                true,
		"169.254.169.254":    true,
		"100.100.100.200":    true,
		"fd00:ec2::254":      true,
		"64:ff9b::a9fe:a9fe": true,
		"0.0.0.0":            true,
		"10.0.0.1":           false,
		"192.168.1.10":       false,
		"fd00::1":            false,
		"100.64.0.1":         false,
		"1.1.1.1":            false,
	} {
		assert.Equal(t, want, HostLocal(netip.MustParseAddr(addr)), addr)
	}
}

func TestDialCheckFailsClosed(t *testing.T) {
	g := New(false)
	assert.NoError(t, g.checkDial("tcp", "1.1.1.1:443", nil))
	assert.Error(t, g.checkDial("tcp", "169.254.169.254:80", nil))

	// The dialer only passes resolved addresses, so a name means something went wrong and is refused
	assert.Error(t, g.checkDial("tcp", "metadata.internal:80", nil))
}

func TestBlockedRangesApplyEvenWherePrivateTargetsAreAllowed(t *testing.T) {
	blocked := []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32"), netip.MustParsePrefix("2001:db8::/32")}
	g := New(true, blocked...)
	assert.NoError(t, g.checkDial("tcp", "10.0.0.1:80", nil), "private targets stay allowed")
	assert.NoError(t, g.checkDial("tcp", "127.0.0.1:11434", nil), "a local Ollama stays reachable")
	assert.Error(t, g.checkDial("tcp", "203.0.113.7:443", nil))
	assert.Error(t, g.checkDial("tcp", "[::ffff:203.0.113.7]:443", nil), "a mapped address reaches the blocked IPv4 address")
	assert.Error(t, g.checkDial("tcp", "[2001:db8::1]:443", nil))
	assert.Error(t, g.checkDial("tcp", "[2001:db8::1%eth0]:443", nil), "a zone doesn't change the address the connection reaches")
	assert.NoError(t, g.checkDial("tcp", "203.0.113.8:443", nil))

	// Without blocked ranges a guard that allows private targets refuses nothing, so it skips the check entirely
	assert.False(t, New(true).checks())
	assert.True(t, g.checks())
}

func TestCheckingGuardsIgnoreTheEnvironmentProxy(t *testing.T) {
	// A proxy connects to the target on the guard's behalf, where the dial check can't see the address
	assert.Nil(t, New(false).transport.Proxy)
	assert.Nil(t, New(true, netip.MustParsePrefix("203.0.113.7/32")).transport.Proxy)

	// A guard that refuses nothing keeps the proxy an instance may need to reach the internet
	assert.NotNil(t, New(true).transport.Proxy)
}

func TestInRangesFollowsEmbeddedIPv4Addresses(t *testing.T) {
	ranges := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	assert.True(t, InRanges(netip.MustParseAddr("198.51.100.20"), ranges))
	assert.True(t, InRanges(netip.MustParseAddr("64:ff9b::c633:6414"), ranges), "a NAT64 address reaches the IPv4 address it embeds")
	assert.False(t, InRanges(netip.MustParseAddr("198.51.101.1"), ranges))
	assert.False(t, InRanges(netip.MustParseAddr("198.51.100.20"), nil))
}

func TestLimitResponsesCountsInflatedBytes(t *testing.T) {
	const limit, errorLimit = 1 << 20, 1 << 10
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		status, _ := strconv.Atoi(r.URL.Query().Get("status"))
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(status)
		zw := gzip.NewWriter(w)
		_, _ = zw.Write(bytes.Repeat([]byte(" "), size))
		_ = zw.Close()
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: LimitResponses(New(true).HTTPClient(0).Transport, limit, errorLimit)}

	read := func(status, size int) (int, error) {
		resp, err := client.Get(srv.URL + "?status=" + strconv.Itoa(status) + "&size=" + strconv.Itoa(size))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.True(t, resp.Uncompressed, "the transport inflates the body before the limit sees it")
		data, err := io.ReadAll(resp.Body)
		return len(data), err
	}

	// A body right at the limit is read whole
	n, err := read(http.StatusOK, limit)
	require.NoError(t, err)
	assert.Equal(t, limit, n)

	// One inflated byte more fails instead of being cut off, although only a few KB crossed the wire
	n, err = read(http.StatusOK, limit+1)
	require.ErrorIs(t, err, ErrResponseTooLarge)
	assert.Equal(t, limit, n)

	// An error body gets the smaller limit
	n, err = read(http.StatusBadGateway, errorLimit)
	require.NoError(t, err)
	assert.Equal(t, errorLimit, n)
	_, err = read(http.StatusBadGateway, errorLimit+1)
	require.ErrorIs(t, err, ErrResponseTooLarge)
}
