//go:build integration

package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/broker"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/sandboxtest"
)

// Tests may share an engine with a running server, so everything they create is scoped to this test process:
//   - instance IDs start with "sbt-<run>-", which the cleanup matches on and never goes beyond
//   - the unrestricted bridge is "ump-unrestricted-sbt-<run>" instead of the ump-unrestricted a server uses
//   - relays are named after the instance, so they never replace a server's relay
// SANDBOXTEST_BROKER_HOST overrides where the relay forwards to, for engines whose host-gateway does not reach this machine
// SANDBOXTEST_RUNTIME runs the sandboxes with another OCI runtime, such as runsc for gVisor

// testHostID is the replica ID of every test adapter
const testHostID = "sandboxtest"

// testRun makes resource names unique per test process, so leftovers of an aborted run never interfere
var testRun = randomID(4)

// newTestAdapter builds an adapter whose instance ID is derived from the test name, as the conformance suite requires
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	port, err := sandboxtest.BrokerPort()
	require.NoError(t, err)
	return newTestAdapterFor(t, port)
}

// newTestAdapterFor builds a test adapter whose sandboxes reach the broker listening on port
func newTestAdapterFor(t *testing.T, port int) *Adapter {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	a, err := New(context.Background(), Config{
		InstanceID:        testInstancePrefix() + hex.EncodeToString(sum[:4]),
		HostID:            testHostID,
		DefaultImage:      sandboxtest.DefaultImage,
		BrokerPort:        port,
		BrokerHost:        os.Getenv("SANDBOXTEST_BROKER_HOST"),
		Runtime:           os.Getenv("SANDBOXTEST_RUNTIME"),
		AllowUnrestricted: true,
		Logger:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	require.NoError(t, err)
	a.unrestricted = "ump-unrestricted-sbt-" + testRun
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// testInstancePrefix starts the instance ID of every adapter this test process creates
func testInstancePrefix() string {
	return "sbt-" + testRun + "-"
}

func TestConformance(t *testing.T) {
	probe := requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })

	// Document what the local engine cannot enforce instead of failing on it
	opts := sandboxtest.Options{}
	if strings.Contains(probe.OperatingSystem, "OrbStack") {
		opts.Skip = map[string]string{
			"SandboxIsolation/UnrestrictedPeer": "OrbStack accepts all forwarded traffic in DOCKER-USER, which disables Docker's inter-network isolation and enable_icc=false",
		}
	}
	opts.OOMStopsSandbox = runtimeIsolation(os.Getenv("SANDBOXTEST_RUNTIME")) == sandbox.IsolationGVisor
	sandboxtest.Run(t, func(t *testing.T) sandbox.Adapter { return newTestAdapter(t) }, opts)
}

func TestSecurityDefaults(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	runID := "sbt-sec-" + randomID(4)
	sb, err := a.Create(ctx, sandbox.Spec{RunID: runID, JobID: "job-sec", WorkspaceID: "ws", Broker: sandbox.BrokerAccess{Token: "tok"}, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	// The container carries the ownership labels and the documented limits
	info, err := a.cli.ContainerInspect(ctx, sb.ID())
	require.NoError(t, err)
	labels := info.Config.Labels
	assert.Equal(t, a.cfg.InstanceID, labels[labelInstance])
	assert.Equal(t, runID, labels[labelRun])
	assert.Equal(t, "job-sec", labels[labelJob])
	assert.Equal(t, testHostID, labels[labelHost])
	assert.NotEmpty(t, labels[labelExpires])
	assert.Contains(t, info.HostConfig.CapDrop, "ALL")
	assert.Contains(t, info.HostConfig.SecurityOpt, "no-new-privileges")
	assert.EqualValues(t, 1024*1024*1024, info.HostConfig.Memory)
	assert.EqualValues(t, 1e9, info.HostConfig.NanoCPUs)
	require.NotNil(t, info.HostConfig.PidsLimit)
	assert.EqualValues(t, 256, *info.HostConfig.PidsLimit)
	assert.Equal(t, []string{sandbox.UmpBinary, "init", "--ttl", "1h0m0s"}, []string(info.Config.Entrypoint))

	// Inside, the agent has no capabilities and cannot gain privileges
	var out strings.Builder
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", "grep -E '^(CapEff|NoNewPrivs)' /proc/self/status"}, Stdout: &out})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	assert.Contains(t, out.String(), "CapEff:\t0000000000000000")
	assert.Contains(t, out.String(), "NoNewPrivs:\t1")

	// An internet sandbox sits on its internal run network alone, where the relay answers as umpteenth, so the egress proxy is its only way out
	runNet, err := a.cli.NetworkInspect(ctx, runNetworkPrefix+runID, network.InspectOptions{})
	require.NoError(t, err)
	assert.True(t, runNet.Internal)
	assert.Contains(t, runNet.Containers, info.ID)
	assert.Len(t, info.NetworkSettings.Networks, 1)
	relay, err := a.cli.ContainerInspect(ctx, a.relayName())
	require.NoError(t, err)
	assert.Contains(t, runNet.Containers, relay.ID)
	assert.Contains(t, relay.NetworkSettings.Networks[runNet.Name].Aliases, brokerAlias)

	// Destroy removes the run's networks along with the container
	require.NoError(t, a.Destroy(ctx, sb.ID()))
	_, err = a.cli.NetworkInspect(ctx, runNetworkPrefix+runID, network.InspectOptions{})
	assert.True(t, isNotFound(err), "the run network must be removed, got %v", err)
}

func TestUnrestrictedSandboxesShareAnIsolatedBridge(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	runID := "sbt-unr-" + randomID(4)
	sb, err := a.Create(ctx, sandbox.Spec{RunID: runID, Network: sandbox.NetworkUnrestricted, Broker: sandbox.BrokerAccess{Token: "tok"}, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	// Unrestricted sandboxes share one bridge without inter-container traffic
	bridge, err := a.cli.NetworkInspect(ctx, a.unrestricted, network.InspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "false", bridge.Options["com.docker.network.bridge.enable_icc"])
	assert.Contains(t, bridge.Containers, sb.ID())

	// The sandbox gets no proxy, since it has a route of its own
	var out strings.Builder
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `echo "proxy=${HTTPS_PROXY:-none}"`}, Stdout: &out})
	require.NoError(t, err)
	assert.Equal(t, "proxy=none", strings.TrimSpace(out.String()))
}

func TestRecreatesRemovedSharedResources(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	// An unrestricted sandbox creates the shared bridge, and then someone removes it and the relay
	first, err := a.Create(ctx, sandbox.Spec{Network: sandbox.NetworkUnrestricted, Broker: sandbox.BrokerAccess{Token: "first"}})
	require.NoError(t, err)
	require.NoError(t, a.Destroy(ctx, first.ID()))
	require.NoError(t, a.cli.NetworkRemove(ctx, a.unrestricted))
	require.NoError(t, a.cli.ContainerRemove(ctx, a.relayName(), container.RemoveOptions{Force: true}))

	// The next sandbox brings both back and works end to end
	sb, err := a.Create(ctx, sandbox.Spec{Network: sandbox.NetworkUnrestricted, Broker: sandbox.BrokerAccess{Token: "heal"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	var out strings.Builder
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `curl -fsS -m 10 -H "Authorization: Bearer $UMP_TOKEN" "$UMP_BROKER_URL/ping"`}, Stdout: &out})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "pong", strings.TrimSpace(out.String()))
	bridge, err := a.cli.NetworkInspect(ctx, a.unrestricted, network.InspectOptions{})
	require.NoError(t, err)
	assert.Contains(t, bridge.Containers, sb.ID())
}

func TestPreparePrunesNetworksOfAPreviousProcess(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()

	// A crash during provisioning leaves a run network behind without a sandbox
	leftover := "ump-run-sbt-" + testRun + "-crashed"
	require.NoError(t, a.createNetwork(ctx, leftover, a.runLabels(sandbox.Spec{RunID: "crashed"}, roleRunNetwork)))

	// A network of a run this process is still provisioning must survive, so it is created after the restart point
	time.Sleep(10 * time.Millisecond)
	a.started = time.Now()
	time.Sleep(10 * time.Millisecond)
	current := "ump-run-sbt-" + testRun + "-provisioning"
	require.NoError(t, a.createNetwork(ctx, current, a.runLabels(sandbox.Spec{RunID: "provisioning"}, roleRunNetwork)))

	require.NoError(t, a.Prepare(ctx))
	_, err := a.cli.NetworkInspect(ctx, leftover, network.InspectOptions{})
	assert.True(t, isNotFound(err), "the leftover network should be pruned, got %v", err)
	_, err = a.cli.NetworkInspect(ctx, current, network.InspectOptions{})
	assert.NoError(t, err, "the network of a run in progress must be kept")
	require.NoError(t, a.removeNetwork(ctx, current))
}

func TestTTLStopsSandbox(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()

	sb, err := a.Create(ctx, sandbox.Spec{TTL: 2 * time.Second, Network: sandbox.NetworkNone})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	// A command outliving the TTL dies with the sandbox, which then counts as gone
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sleep", "30"}, Timeout: 20 * time.Second})
	assert.ErrorIs(t, err, sandbox.ErrSandboxGone)

	// The reaper still sees it until it is destroyed
	list, err := a.List(ctx)
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(list, func(s sandbox.Summary) bool { return s.ID == sb.ID() }))
}

func TestBuildTimeoutStopsStep(t *testing.T) {
	requireEngine(t)
	a := newTestAdapter(t)
	ctx := t.Context()

	// A build cut short by its timeout must not leave its step running, or the step would still land in the cache
	marker := "sbt-step-" + randomID(4)
	tag := "umpteenth-sandboxtest/timeout:" + marker
	t.Cleanup(func() { _ = a.RemoveImage(context.Background(), tag) })
	_, err := a.BuildImage(ctx, sandbox.BuildSpec{
		Dockerfile: "FROM " + sandboxtest.DefaultImage + "\nRUN sleep 120 && echo " + marker + "\n",
		Tag:        tag,
		Timeout:    3 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")

	// Removal is asynchronous in the engine, so give it a moment
	assert.Eventually(t, func() bool {
		containers, err := a.cli.ContainerList(ctx, container.ListOptions{All: true})
		if err != nil {
			return false
		}
		return !slices.ContainsFunc(containers, func(c container.Summary) bool { return strings.Contains(c.Command, marker) })
	}, 10*time.Second, 200*time.Millisecond, "the abandoned build step is still there")
}

// engineInfo is what the tests need to know about the engine
type engineInfo struct {
	OperatingSystem string
}

// requireEngine skips when no engine is reachable, which keeps the tag usable on machines without Docker
func requireEngine(t *testing.T) engineInfo {
	t.Helper()
	a, err := New(t.Context(), Config{InstanceID: "probe", HostID: testHostID, DefaultImage: sandboxtest.DefaultImage})
	if err != nil {
		t.Skipf("no container engine: %v", err)
	}
	defer func() { _ = a.Close() }()
	info, err := a.cli.Info(t.Context())
	require.NoError(t, err)
	return engineInfo{OperatingSystem: info.OperatingSystem}
}

// cleanupTestResources removes what this test process created, and nothing else, and fails if sandboxes or run networks were left behind
func cleanupTestResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, err := New(ctx, Config{InstanceID: "cleanup", HostID: testHostID, DefaultImage: sandboxtest.DefaultImage})
	if err != nil {
		t.Errorf("cleanup: %v", err)
		return
	}
	defer func() { _ = a.Close() }()
	ours := func(labels map[string]string) bool {
		return strings.HasPrefix(labels[labelInstance], testInstancePrefix())
	}

	// Sandboxes must already be gone since every test destroys what it creates, while relays are expected and removed quietly
	containers, err := a.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", labelInstance))})
	require.NoError(t, err)
	for _, c := range containers {
		if !ours(c.Labels) {
			continue
		}
		if c.Labels[labelRole] == roleSandbox {
			t.Errorf("sandbox %s of %s was left behind", c.ID, c.Labels[labelInstance])
		}
		if err := a.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil && !isNotFound(err) {
			t.Errorf("failed to remove test container %s: %v", c.ID, err)
		}
	}

	// Run networks must be gone too
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", labelRole+"="+roleRunNetwork))})
	require.NoError(t, err)
	for _, n := range networks {
		if ours(n.Labels) {
			t.Errorf("network %s was left behind", n.Name)
			_ = a.removeNetwork(ctx, n.ID)
		}
	}

	// The per-process unrestricted bridge goes last, once nothing of ours is attached
	if err := a.removeNetwork(ctx, "ump-unrestricted-sbt-"+testRun); err != nil {
		t.Errorf("failed to remove the test unrestricted network: %v", err)
	}
}

func TestRunNetworksKeepSandboxesOffTheHost(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))
	port := 20000 + int(time.Now().UnixNano()%1000)
	startHostServer(t, a, port)

	for _, policy := range []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkAllowlist, sandbox.NetworkInternet} {
		runID := "sbt-host-" + randomID(4)
		sb, err := a.Create(ctx, sandbox.Spec{RunID: runID, JobID: "job-host", WorkspaceID: "ws", Network: policy, Broker: sandbox.BrokerAccess{Token: "tok"}, TTL: time.Hour})
		require.NoError(t, err)

		// The host has no address on the run network, so the first one of its subnet, where Docker would put the host, doesn't lead there
		runNet, err := a.cli.NetworkInspect(ctx, runNetworkPrefix+runID, network.InspectOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, runNet.IPAM.Config)
		subnet, err := netip.ParsePrefix(runNet.IPAM.Config[0].Subnet)
		require.NoError(t, err)
		var out strings.Builder
		_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", fmt.Sprintf("sleep 1; curl --noproxy '*' -s -o /dev/null -w '%%{http_code}' -m 5 http://%s:%d/ || true", subnet.Masked().Addr().Next(), port)}, Stdout: &out})
		require.NoError(t, err)
		assert.Equal(t, "000", out.String(), policy)

		// The broker still answers on the run network
		out.Reset()
		res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `curl -fsS -m 10 -H "Authorization: Bearer $UMP_TOKEN" "$UMP_BROKER_URL/ping"`}, Stdout: &out})
		require.NoError(t, err)
		assert.Equal(t, 0, res.ExitCode, policy)
		assert.Equal(t, "pong", strings.TrimSpace(out.String()), policy)
		require.NoError(t, a.Destroy(ctx, sb.ID()))
	}
}

// startHostServer runs a web server in the Docker host's network namespace, which stands for a service the host listens on
func startHostServer(t *testing.T, a *Adapter, port int) {
	t.Helper()
	ctx := t.Context()
	server, err := a.cli.ContainerCreate(ctx, &container.Config{
		Image:      sandboxtest.DefaultImage,
		Entrypoint: []string{"python3", "-m", "http.server", strconv.Itoa(port), "--bind", "0.0.0.0"},
		Labels:     map[string]string{labelInstance: a.cfg.InstanceID, labelRole: "test-server"},
	}, &container.HostConfig{NetworkMode: "host"}, nil, nil, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), server.ID, container.RemoveOptions{Force: true})
	})
	require.NoError(t, a.cli.ContainerStart(ctx, server.ID, container.StartOptions{}))
	time.Sleep(time.Second)
}

func TestImageBuildsLeaveThroughTheEgressProxy(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	port, registry := startRealBroker(t, nil)
	a := newTestAdapterFor(t, port)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))
	token := "build-" + randomID(8)
	revoke := registry.GrantProxy(token, &runner.ProxyGrant{Network: sandbox.NetworkInternet})
	t.Cleanup(revoke)

	// The step reports what it reached through the proxy, past it, and on the private network
	step := `(curl -s -o /dev/null -w 'PROXY %{http_code}\n' -m 20 http://example.com/ || true)` +
		` && (curl --noproxy '*' -s -o /dev/null -m 5 http://example.com/ && echo DIRECT open || echo DIRECT blocked)` +
		` && (curl -s -m 10 http://10.255.255.1/ | grep -q 'does not connect' && echo PRIVATE blocked || echo PRIVATE open)`
	build := func(broker sandbox.BrokerAccess) string {
		t.Helper()
		var logs strings.Builder
		tag := "umpteenth-sbt-" + testRun + "/build-net:" + randomID(4)

		// A marker per build keeps any earlier build from answering out of the cache, since the builder leaves the proxy arguments out of its cache key
		_, err := a.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: fmt.Sprintf("FROM %s\nRUN %s && echo %s\n", sandboxtest.DefaultImage, step, randomID(4)), Tag: tag, Logs: &logs, Timeout: 2 * time.Minute, Broker: broker})
		require.NoError(t, err, logs.String())
		t.Cleanup(func() { _ = a.RemoveImage(context.WithoutCancel(ctx), tag) })
		return logs.String()
	}

	// With a proxy grant the step reaches the internet through the proxy, but has no route of its own and the proxy keeps it off private networks
	logs := build(sandbox.BrokerAccess{Token: token})
	if hostHasInternet() {
		assert.Contains(t, logs, "PROXY 200", logs)
	}
	assert.Contains(t, logs, "DIRECT blocked", logs)
	assert.Contains(t, logs, "PRIVATE blocked", logs)

	// Without one it has no network at all
	logs = build(sandbox.BrokerAccess{})
	assert.Contains(t, logs, "PROXY 000", logs)
	assert.Contains(t, logs, "DIRECT blocked", logs)

	// The build network goes with the build
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", labelInstance+"="+a.cfg.InstanceID), filters.Arg("label", labelRole+"="+roleRunNetwork))})
	require.NoError(t, err)
	assert.Empty(t, networks)
}

func TestProxiedSandboxesLeaveThroughTheBroker(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	var a *Adapter
	port, registry := startRealBroker(t, func(addr netip.Addr) bool { return a.ContainerAddress(addr) })
	a = newTestAdapterFor(t, port)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))
	if !hostHasInternet() {
		t.Skip("reaching the internet through the proxy needs internet access")
	}

	token := "tok-" + randomID(8)
	revoke := registry.GrantProxy(token, &runner.ProxyGrant{Network: sandbox.NetworkInternet})
	t.Cleanup(revoke)
	sb, err := a.Create(ctx, sandbox.Spec{Network: sandbox.NetworkInternet, Broker: sandbox.BrokerAccess{Token: token}, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.WithoutCancel(ctx), sb.ID()) })

	// HTTP and HTTPS go through the HTTP proxy, Node's fetch included, other TCP through SOCKS5 or ump connect, and private addresses stay out of reach
	script := `curl -s -o /dev/null -w 'http %{http_code}\n' -m 20 http://example.com/
curl -s -o /dev/null -w 'https %{http_code}\n' -m 20 https://example.com/
node -e "fetch('https://example.com/').then(r => console.log('node', r.status), e => console.log('node', e.cause?.code))"
curl -s -o /dev/null -w 'socks %{http_code}\n' -m 20 -x "$ALL_PROXY" https://example.com/
printf 'HEAD / HTTP/1.0\r\nHost: example.com\r\n\r\n' | timeout 20 ump connect example.com 80 | head -n 1 | cut -d' ' -f2 | sed 's/^/connect /'
curl -s -m 10 http://10.255.255.1/ | grep -q 'does not connect' && echo 'private blocked' || echo 'private open'`
	var out, stderr strings.Builder
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", script}, Stdout: &out, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, []string{"http 200", "https 200", "node 200", "socks 200", "connect 200", "private blocked"}, strings.Split(strings.TrimSpace(out.String()), "\n"), stderr.String())

	// A container on the engine's default bridge stands for a database next to Umpteenth, which this process could reach
	server, err := a.cli.ContainerCreate(ctx, &container.Config{
		Image:      sandboxtest.DefaultImage,
		Entrypoint: []string{"python3", "-m", "http.server", "8000", "--bind", "0.0.0.0"},
		Labels:     map[string]string{labelInstance: a.cfg.InstanceID, labelRole: "test-server"},
	}, &container.HostConfig{NetworkMode: "bridge"}, nil, nil, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), server.ID, container.RemoveOptions{Force: true})
	})
	require.NoError(t, a.cli.ContainerStart(ctx, server.ID, container.StartOptions{}))
	inspect, err := a.cli.ContainerInspect(ctx, server.ID)
	require.NoError(t, err)
	bridgeEndpoint := inspect.NetworkSettings.Networks["bridge"]
	require.NotNil(t, bridgeEndpoint)
	serverIP := bridgeEndpoint.IPAddress
	require.NotEmpty(t, serverIP)

	// Even a job that may reach the private network can't reach it through the proxy
	privateToken := "tok-" + randomID(8)
	t.Cleanup(registry.GrantProxy(privateToken, &runner.ProxyGrant{Network: sandbox.NetworkInternet, AllowPrivateNetwork: true}))
	private, err := a.Create(ctx, sandbox.Spec{Network: sandbox.NetworkInternet, Broker: sandbox.BrokerAccess{Token: privateToken}, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.WithoutCancel(ctx), private.ID()) })
	out.Reset()
	_, err = private.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", "sleep 1; curl -s -m 10 http://" + serverIP + ":8000/ | grep -q 'does not connect' && echo 'container blocked' || echo 'container open'"}, Stdout: &out})
	require.NoError(t, err)
	assert.Equal(t, "container blocked", strings.TrimSpace(out.String()))
}

// startRealBroker serves the actual broker, egress proxy included, on a port the relay can forward to
// containerAddress is how the proxy recognizes containers, as the adapter reports them in production
func startRealBroker(t *testing.T, containerAddress func(netip.Addr) bool) (int, *runner.Registry) {
	t.Helper()
	addr := "0.0.0.0:0"
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr) // #nosec G102 -- the relay reaches the test broker through the Docker host, like the stub broker
	require.NoError(t, err)
	registry := runner.NewRegistry()
	b := broker.New(broker.Dependencies{Live: registry, ContainerAddress: containerAddress})
	srv := &http.Server{Handler: b.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(b.Listener(ln)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port, registry
}

// hostHasInternet decides whether internet-positive checks can run at all
func hostHasInternet() bool {
	conn, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestArchiveDoesNotFollowASymlinkedDirectory(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	sb, err := a.Create(ctx, sandbox.Spec{Network: sandbox.NetworkNone, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.WithoutCancel(ctx), sb.ID()) })

	// A file only root may read, and an agent that points its outputs directory at it
	run := func(user sandbox.User, cmd string) {
		res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", cmd}, User: user})
		require.NoError(t, err)
		require.Equal(t, 0, res.ExitCode, cmd)
	}
	run(sandbox.UserRoot, "mkdir -p /root/private && echo top-secret > /root/private/key && chmod -R go-rwx /root/private")
	run(sandbox.UserAgent, "mkdir -p /ump && rm -rf /ump/outputs && ln -s /root/private /ump/outputs")

	// The host collects outputs as root, so it must not follow the link
	rc, err := sb.Archive(ctx, "/ump/outputs", 1<<20)
	if err == nil {
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		assert.NotContains(t, string(data), "top-secret")
	}
	assert.Error(t, err)
}
