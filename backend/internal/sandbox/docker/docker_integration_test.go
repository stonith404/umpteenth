//go:build integration

package docker

import (
	"context"
	"encoding/base64"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
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

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/umpbin"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/sandboxtest"
)

// Tests may share an engine with a running server, so everything they create is scoped to this test process:
//   - instance IDs start with "sbt-<run>-", which the cleanup matches on and never goes beyond
//   - the egress bridge is "ump-egress-sbt-<run>" instead of the ump-egress a server uses
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

	sum := sha256.Sum256([]byte(t.Name()))
	a, err := New(context.Background(), Config{
		InstanceID:   testInstancePrefix() + hex.EncodeToString(sum[:4]),
		HostID:       testHostID,
		DefaultImage: sandboxtest.DefaultImage,
		BrokerPort:   port,
		BrokerHost:   os.Getenv("SANDBOXTEST_BROKER_HOST"),
		Runtime:      os.Getenv("SANDBOXTEST_RUNTIME"),
		UmpBinary:    umpbin.Binary,
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	require.NoError(t, err)
	a.egress = "ump-egress-sbt-" + testRun
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
			"SandboxIsolation/InternetPeer": "OrbStack accepts all forwarded traffic in DOCKER-USER, which disables Docker's inter-network isolation and enable_icc=false",
		}
	}
	opts.OOMStopsSandbox = runtimeIsolation(os.Getenv("SANDBOXTEST_RUNTIME")) == sandbox.IsolationGVisor
	sandboxtest.RunWithOptions(t, func(t *testing.T) sandbox.Adapter { return newTestAdapter(t) }, opts)
}

func TestSecurityDefaults(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	port, err := sandboxtest.BrokerPort()
	require.NoError(t, err)
	runID := "sbt-sec-" + randomID(4)
	sb, err := a.Create(ctx, sandbox.Spec{RunID: runID, JobID: "job-sec", WorkspaceID: "ws", Broker: sandbox.BrokerAccess{Token: "tok", Port: port}, TTL: time.Hour})
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

	// The sandbox sits on its internal run network and the egress bridge, and the relay answers as umpteenth on the run network
	runNet, err := a.cli.NetworkInspect(ctx, runNetworkPrefix+runID, network.InspectOptions{})
	require.NoError(t, err)
	assert.True(t, runNet.Internal)
	assert.Contains(t, runNet.Containers, info.ID)
	relay, err := a.cli.ContainerInspect(ctx, a.relayName())
	require.NoError(t, err)
	assert.Contains(t, runNet.Containers, relay.ID)
	assert.Contains(t, relay.NetworkSettings.Networks[runNet.Name].Aliases, brokerAlias)
	egress, err := a.cli.NetworkInspect(ctx, a.egress, network.InspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "false", egress.Options["com.docker.network.bridge.enable_icc"])
	assert.Contains(t, egress.Containers, info.ID)

	// Destroy removes the run network along with the container
	require.NoError(t, a.Destroy(ctx, sb.ID()))
	_, err = a.cli.NetworkInspect(ctx, runNetworkPrefix+runID, network.InspectOptions{})
	assert.True(t, isNotFound(err), "the run network must be removed, got %v", err)
}

func TestRecreatesRemovedSharedResources(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	// Someone removes the egress bridge and the relay after the adapter prepared them
	require.NoError(t, a.cli.NetworkRemove(ctx, a.egress))
	require.NoError(t, a.cli.ContainerRemove(ctx, a.relayName(), container.RemoveOptions{Force: true}))

	// The next sandbox brings both back and works end to end
	port, err := sandboxtest.BrokerPort()
	require.NoError(t, err)
	sb, err := a.Create(ctx, sandbox.Spec{Broker: sandbox.BrokerAccess{Token: "heal", Port: port}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	var out strings.Builder
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `curl -fsS -m 10 -H "Authorization: Bearer $UMP_TOKEN" "$UMP_BROKER_URL/ping"`}, Stdout: &out})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "pong", strings.TrimSpace(out.String()))
	egress, err := a.cli.NetworkInspect(ctx, a.egress, network.InspectOptions{})
	require.NoError(t, err)
	assert.Contains(t, egress.Containers, sb.ID())
}

func TestPreparePrunesNetworksOfAPreviousProcess(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()

	// A crash during provisioning leaves a run network behind without a sandbox
	leftover := "ump-run-sbt-" + testRun + "-crashed"
	require.NoError(t, a.createNetwork(ctx, leftover, true, a.runLabels(sandbox.Spec{}, "crashed", roleRunNetwork)))

	// A network of a run this process is still provisioning must survive, so it is created after the restart point
	time.Sleep(10 * time.Millisecond)
	a.started = time.Now()
	time.Sleep(10 * time.Millisecond)
	current := "ump-run-sbt-" + testRun + "-provisioning"
	require.NoError(t, a.createNetwork(ctx, current, true, a.runLabels(sandbox.Spec{}, "provisioning", roleRunNetwork)))

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
	_, err = a.Get(ctx, sb.ID())
	assert.ErrorIs(t, err, sandbox.ErrNotFound)

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
	a, err := New(t.Context(), Config{InstanceID: "probe", HostID: testHostID, DefaultImage: sandboxtest.DefaultImage, UmpBinary: umpbin.Binary})
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
	a, err := New(ctx, Config{InstanceID: "cleanup", HostID: testHostID, DefaultImage: sandboxtest.DefaultImage, UmpBinary: umpbin.Binary})
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

	// The per-process egress bridges go last, once nothing of ours is attached
	for _, name := range []string{"ump-egress-sbt-" + testRun, "ump-egress-sbt-" + testRun + "-private"} {
		if err := a.removeNetwork(ctx, name); err != nil {
			t.Errorf("failed to remove the test egress network %s: %v", name, err)
		}
	}
}

func TestEgressFirewallKeepsInternetSandboxesOffPrivateNetworks(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))
	info, err := a.Check(ctx)
	require.NoError(t, err)
	if info.EgressFilter != sandbox.EgressFilterActive {
		t.Skipf("the egress firewall is not available on this engine: %s", info.EgressFilterError)
	}

	// A web server on the host stands for a service on the private network, reachable through each bridge's gateway
	port := 18000 + int(time.Now().UnixNano()%1000)
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
	gateway := func(name string) string {
		n, err := a.cli.NetworkInspect(ctx, name, network.InspectOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, n.IPAM.Config)
		return n.IPAM.Config[0].Gateway
	}

	brokerPort, err := sandboxtest.BrokerPort()
	require.NoError(t, err)
	status := func(allowPrivate bool, url string) string {
		t.Helper()
		sb, err := a.Create(ctx, sandbox.Spec{
			RunID: "sbt-fw-" + randomID(4), JobID: "job-fw", WorkspaceID: "ws", Network: sandbox.NetworkInternet, AllowPrivateNetwork: allowPrivate,
			Broker: sandbox.BrokerAccess{Token: "tok", Port: brokerPort}, TTL: time.Hour,
		})
		require.NoError(t, err)
		defer func() { _ = a.Destroy(context.WithoutCancel(ctx), sb.ID()) }()
		var out strings.Builder
		_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", "sleep 1; curl -s -o /dev/null -w '%{http_code}' -m 5 " + url + " || true"}, Stdout: &out})
		require.NoError(t, err)
		return out.String()
	}

	// A sandbox with plain internet access can't reach the host, and one allowed on the private network can
	assert.Equal(t, "000", status(false, fmt.Sprintf("http://%s:%d/", gateway(a.egress), port)))
	assert.Equal(t, "200", status(true, fmt.Sprintf("http://%s:%d/", gateway(a.egressPrivate()), port)))
}

func TestImageBuildsCantReachPrivateNetworks(t *testing.T) {
	requireEngine(t)
	t.Cleanup(func() { cleanupTestResources(t) })
	a := newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))
	info, err := a.Check(ctx)
	require.NoError(t, err)
	if info.EgressFilter != sandbox.EgressFilterActive || a.podman {
		t.Skip("the egress firewall is not available on this engine")
	}

	// A web server on the host stands for a service on the private network, which a build step reaches through its gateway
	port := 19000 + int(time.Now().UnixNano()%1000)
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

	// The step prints what the host answered through the build container's default gateway, and that the internet still answers
	probe := `import struct, socket, urllib.request
gw = next(socket.inet_ntoa(struct.pack("<L", int(f[2], 16))) for f in (l.split() for l in open("/proc/net/route").readlines()[1:]) if f[1] == "00000000")
try:
    print("STATUS", urllib.request.urlopen("http://%s:` + strconv.Itoa(port) + `/" % gw, timeout=5).status)
except Exception as e:
    print("STATUS blocked", type(e).__name__)
print("INTERNET", urllib.request.urlopen("http://example.com/", timeout=10).status)`
	var logs strings.Builder
	tag := "umpteenth-sbt-" + testRun + "/build-net:" + randomID(4)
	dockerfile := fmt.Sprintf("FROM %s\nRUN echo %s | base64 -d | python3\n", sandboxtest.DefaultImage, base64.StdEncoding.EncodeToString([]byte(probe)))
	_, err = a.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: dockerfile, Tag: tag, Logs: &logs, Timeout: 2 * time.Minute})
	require.NoError(t, err, logs.String())
	t.Cleanup(func() { _ = a.RemoveImage(context.WithoutCancel(ctx), tag) })
	assert.Contains(t, logs.String(), "STATUS blocked", logs.String())
	assert.Contains(t, logs.String(), "INTERNET 200", logs.String())
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
