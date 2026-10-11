//go:build unit

package docker

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestContainerIDPatternMatchesEnginePaths(t *testing.T) {
	id := strings.Repeat("ab", 32)
	for _, line := range []string{
		"/var/lib/docker/containers/" + id + "/hostname",
		"/var/lib/containers/storage/overlay-containers/" + id + "/userdata/hostname",
		"0::/machine.slice/libpod-" + id + ".scope",
		"/libpod_parent/libpod-" + id,
	} {
		m := containerIDPattern.FindStringSubmatch(line)
		if assert.NotNil(t, m, line) {
			assert.Equal(t, id, m[1], line)
		}
	}
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

// brokerEngine accepts every broker connect and answers the inspection after it with the given status and endpoints
func brokerEngine(t *testing.T, inspectStatus int, endpoints map[string]network.EndpointResource) *client.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{version}/networks/{id}/connect", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /{version}/networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if inspectStatus != http.StatusOK {
			http.Error(w, `{"message":"engine hiccup"}`, inspectStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(network.Inspect{Name: r.PathValue("id"), Containers: endpoints})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func TestConnectBrokerMarksTheReplicaAddress(t *testing.T) {
	a := &Adapter{cfg: Config{Runtime: defaultRuntime}, selfID: "self", cli: brokerEngine(t, http.StatusOK, map[string]network.EndpointResource{"self": {IPv4Address: "172.30.0.2/16"}})}
	addr, err := a.connectBroker(t.Context(), "ump-run-1")
	require.NoError(t, err)
	assert.Equal(t, netip.MustParseAddr("172.30.0.2"), addr)
	assert.True(t, a.SandboxFacing(addr))
}

// A successful connect alone leaves the server without the address it refuses everything but the broker on
func TestConnectBrokerFailsWithoutTheReplicaAddress(t *testing.T) {
	cases := map[string]*client.Client{
		"inspection fails": brokerEngine(t, http.StatusInternalServerError, nil),
		"endpoint missing": brokerEngine(t, http.StatusOK, map[string]network.EndpointResource{}),
		"address missing":  brokerEngine(t, http.StatusOK, map[string]network.EndpointResource{"self": {}}),
	}
	for name, cli := range cases {
		t.Run(name, func(t *testing.T) {
			a := &Adapter{cfg: Config{Runtime: defaultRuntime}, selfID: "self", cli: cli}
			_, err := a.connectBroker(t.Context(), "ump-run-1")
			require.Error(t, err)
		})
	}
}
