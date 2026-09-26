package docker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// ensureEgress creates the shared egress bridge once; Podman gets per-run egress networks instead
func (a *Adapter) ensureEgress(ctx context.Context) error {
	if a.podman {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.egressCreated {
		return nil
	}

	// Disabling inter-container traffic keeps sandboxes on the shared bridges from reaching each other
	// IPv6 stays off even where the engine enables it by default, since the firewall rules only cover IPv4
	// Jobs that may reach the private network get a bridge of their own, which the firewall treats differently
	for _, name := range []string{a.egress, a.egressPrivate()} {
		_, err := a.cli.NetworkCreate(ctx, name, network.CreateOptions{
			Driver:     "bridge",
			Options:    map[string]string{"com.docker.network.bridge.enable_icc": "false"},
			Labels:     map[string]string{labelRole: roleEgress},
			EnableIPv6: new(false),
		})
		if err != nil && !isConflict(err) {
			return fmt.Errorf("failed to create the %s network: %w", name, err)
		}
	}
	a.egressCreated = true
	return nil
}

// egressFor returns the network that gives a run internet access, creating it first on Podman
// Podman's bridge has no enable_icc option, so each run gets its own egress bridge isolated from all other networks
func (a *Adapter) egressFor(ctx context.Context, spec sandbox.Spec, runID string) (string, error) {
	if !a.podman {
		if spec.AllowPrivateNetwork {
			return a.egressPrivate(), nil
		}
		return a.egress, nil
	}
	name := "ump-egress-" + runID
	return name, a.createNetwork(ctx, name, false, a.runLabels(spec, runID, roleRunNetwork))
}

// createNetwork creates a network, accepting an existing one only if this instance created it for the same run
func (a *Adapter) createNetwork(ctx context.Context, name string, internal bool, labels map[string]string) error {
	// IPv6 stays off even where the engine enables it by default, since the egress firewall only guards IPv4
	opts := network.CreateOptions{Driver: "bridge", Internal: internal, Labels: labels, EnableIPv6: new(false)}
	if a.podman {
		// Podman routes between bridge networks unless they are isolated
		opts.Options = map[string]string{"isolate": "true"}
	}
	_, err := a.cli.NetworkCreate(ctx, name, opts)
	if err == nil {
		return nil
	}
	if isConflict(err) {
		existing, inspectErr := a.cli.NetworkInspect(ctx, name, network.InspectOptions{})
		if inspectErr == nil && existing.Labels[labelInstance] == labels[labelInstance] && existing.Labels[labelRun] == labels[labelRun] {
			return nil
		}
	}
	return fmt.Errorf("failed to create network %s: %w", name, err)
}

// removeRunNetworks removes every network this instance created for a run, detaching the broker endpoint first
func (a *Adapter) removeRunNetworks(ctx context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", labelInstance+"="+a.cfg.InstanceID),
		filters.Arg("label", labelRun+"="+runID),
	)})
	if err != nil {
		return fmt.Errorf("failed to list the networks of run %s: %w", runID, err)
	}

	var errs []error
	for _, n := range networks {
		if err := a.removeNetwork(ctx, n.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeNetwork disconnects every remaining endpoint, such as the relay or this replica's container, and removes the network
func (a *Adapter) removeNetwork(ctx context.Context, id string) error {
	info, err := a.cli.NetworkInspect(ctx, id, network.InspectOptions{})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect network %s: %w", id, err)
	}
	for endpoint := range info.Containers {
		if err := a.cli.NetworkDisconnect(ctx, id, endpoint, true); err != nil && !isNotFound(err) {
			a.log.DebugContext(ctx, "Failed to disconnect from a sandbox network", "network", info.Name, "container", endpoint, "error", err)
		}
	}
	if err := a.cli.NetworkRemove(ctx, id); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to remove network %s: %w", info.Name, err)
	}
	a.mu.Lock()
	delete(a.sandboxFacing, info.Name)
	a.mu.Unlock()
	return nil
}

// pruneOrphanNetworks removes run networks of this replica whose sandbox container no longer exists, e.g. after a crash during Create
// Only networks created before createdBefore are considered, since a newer one may belong to a run that is still creating its sandbox
func (a *Adapter) pruneOrphanNetworks(ctx context.Context, createdBefore time.Time) {
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", labelInstance+"="+a.cfg.InstanceID),
		filters.Arg("label", labelHost+"="+a.cfg.HostID),
		filters.Arg("label", labelRole+"="+roleRunNetwork),
	)})
	if err != nil {
		a.log.WarnContext(ctx, "Failed to list sandbox networks", "error", err)
		return
	}

	for _, n := range networks {
		if !n.Created.Before(createdBefore) {
			continue
		}
		containers, err := a.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(
			filters.Arg("label", labelInstance+"="+a.cfg.InstanceID),
			filters.Arg("label", labelRole+"="+roleSandbox),
			filters.Arg("label", labelRun+"="+n.Labels[labelRun]),
		)})
		if err != nil || len(containers) > 0 {
			continue
		}
		if err := a.removeNetwork(ctx, n.ID); err != nil {
			a.log.WarnContext(ctx, "Failed to prune an orphaned sandbox network", "network", n.Name, "error", err)
		}
	}
}

// brokerPort returns the port sandboxes use for the broker; the relay only listens on the configured one
func (a *Adapter) brokerPort(ctx context.Context, requested int) (int, error) {
	if requested == 0 || requested == a.cfg.BrokerPort {
		return a.cfg.BrokerPort, nil
	}
	a.mu.Lock()
	a.detectModeLocked(ctx)
	relay := a.selfID == ""
	a.mu.Unlock()
	if relay {
		return 0, fmt.Errorf("the broker relay forwards port %d, not the requested port %d", a.cfg.BrokerPort, requested)
	}
	return requested, nil
}

// ensureBrokerPath detects the deployment mode once and, in binary mode, makes sure the relay is running
// The relay is inspected on every call, so one removed by hand is recreated by the next sandbox
func (a *Adapter) ensureBrokerPath(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.detectModeLocked(ctx)
	if a.selfID != "" {
		return nil
	}
	return a.ensureRelayLocked(ctx)
}

// detectModeLocked decides between container mode and binary mode the first time it is called
// Callers must hold a.mu
func (a *Adapter) detectModeLocked(ctx context.Context) {
	if a.modeDetected {
		return
	}
	a.selfID = a.detectSelf(ctx)
	a.modeDetected = true
	if a.selfID != "" {
		a.log.InfoContext(ctx, "Sandboxes reach the broker through this container", "container", a.selfID)
	} else {
		a.log.InfoContext(ctx, "Sandboxes reach the broker through a relay container", "relay", a.relayName(), "target", a.relayTarget())
	}
}

// connectBroker joins the broker endpoint to a run network under the umpteenth alias, and returns its address there when the engine reports one
func (a *Adapter) connectBroker(ctx context.Context, runNet string) (netip.Addr, error) {
	a.mu.Lock()
	endpoint := a.selfID
	if endpoint == "" {
		endpoint = a.relayID
	}
	a.mu.Unlock()

	err := a.cli.NetworkConnect(ctx, runNet, endpoint, &network.EndpointSettings{Aliases: []string{brokerAlias}})

	// Connecting twice fails, which is fine when the endpoint is already there
	info, inspectErr := a.cli.NetworkInspect(ctx, runNet, network.InspectOptions{})
	joined, ok := info.Containers[endpoint]
	if err != nil && (inspectErr != nil || !ok) {
		return netip.Addr{}, fmt.Errorf("failed to connect the broker to %s: %w", runNet, err)
	}
	if !ok {
		return netip.Addr{}, nil
	}

	// In container mode this replica now has an address sandboxes can reach, which only the broker may answer on
	if endpoint == a.selfID {
		a.markSandboxFacing(runNet, joined.IPv4Address)
	}
	// An address the engine reports in an unexpected shape only costs the /etc/hosts entry, not the sandbox
	if prefix, parseErr := netip.ParsePrefix(joined.IPv4Address); parseErr == nil {
		return prefix.Addr(), nil
	}
	return netip.Addr{}, nil
}

// markSandboxFacing remembers this replica's address on a run network
func (a *Adapter) markSandboxFacing(runNet, cidr string) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sandboxFacing == nil {
		a.sandboxFacing = map[string]netip.Addr{}
	}
	a.sandboxFacing[runNet] = prefix.Addr()
}

// loadSandboxFacing finds this replica's addresses on run networks it was attached to before it started, which Docker keeps across restarts
func (a *Adapter) loadSandboxFacing(ctx context.Context) {
	a.mu.Lock()
	self := a.selfID
	a.mu.Unlock()
	if self == "" {
		return
	}
	info, err := a.cli.ContainerInspect(ctx, self)
	if err != nil || info.NetworkSettings == nil {
		return
	}
	for name, ep := range info.NetworkSettings.Networks {
		if strings.HasPrefix(name, runNetworkPrefix) && ep != nil && ep.IPAddress != "" {
			a.markSandboxFacing(name, ep.IPAddress+"/32")
		}
	}
}

// SandboxFacing reports whether an address is one of this replica's addresses on a run network
// The server refuses API and other non-broker connections on them, so a sandbox reaches nothing but the broker (PLAN.md §4.7.3)
func (a *Adapter) SandboxFacing(addr netip.Addr) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, own := range a.sandboxFacing {
		if own == addr {
			return true
		}
	}
	return false
}

// relayName scopes the relay to this replica and instance, so two installations on one engine never fight over it
func (a *Adapter) relayName() string {
	sum := sha256.Sum256([]byte(a.cfg.InstanceID))
	return relayNamePrefix + a.cfg.HostID + "-" + hex.EncodeToString(sum[:4])
}

// relayTarget is where the relay forwards connections to: the broker on the host
func (a *Adapter) relayTarget() string {
	host := a.cfg.BrokerHost
	if host == "" {
		host = "host.docker.internal"
		if a.podman {
			host = "host.containers.internal"
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(a.cfg.BrokerPort))
}

// ensureRelayLocked makes sure this replica's relay container runs the current ump binary with the current target
// Callers must hold a.mu
func (a *Adapter) ensureRelayLocked(ctx context.Context) error {
	name := a.relayName()
	target := a.relayTarget()

	arch, err := a.imageArch(ctx, a.cfg.DefaultImage)
	if err != nil {
		return err
	}
	ump, err := a.cfg.UmpBinary(arch)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(ump)
	digest := hex.EncodeToString(sum[:])

	// Reuse a relay that matches, restart a stopped one, and replace an outdated one
	info, err := a.cli.ContainerInspect(ctx, name)
	switch {
	case err == nil && info.Config != nil && info.Config.Image == a.cfg.DefaultImage &&
		info.Config.Labels[labelRelayTarget] == target && info.Config.Labels[labelUmpDigest] == digest:
		if info.State == nil || !info.State.Running {
			if err := a.cli.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("failed to start the broker relay: %w", err)
			}
		}
		a.relayID = info.ID
		return nil
	case err == nil:
		if err := a.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true}); err != nil && !isNotFound(err) {
			return fmt.Errorf("failed to replace the outdated broker relay: %w", err)
		}
	case !isNotFound(err):
		return fmt.Errorf("failed to inspect the broker relay: %w", err)
	}

	id, err := a.createRelay(ctx, name, target, digest, ump)
	if isConflict(err) {
		// Another adapter in this process or replica won the race, so use its relay
		existing, inspectErr := a.cli.ContainerInspect(ctx, name)
		if inspectErr != nil {
			return fmt.Errorf("failed to create the broker relay: %w", err)
		}
		a.relayID = existing.ID
		return nil
	}
	if err != nil {
		return err
	}
	a.relayID = id

	// Runs that are still active lost their broker path with the old relay, so rejoin their networks
	a.reconnectRunNetworksLocked(ctx)
	return nil
}

// createRelay creates and starts the relay container on the default bridge, from where the host is reachable
func (a *Adapter) createRelay(ctx context.Context, name, target, digest string, ump []byte) (string, error) {
	port := strconv.Itoa(a.cfg.BrokerPort)
	cfg := &container.Config{
		Image:      a.cfg.DefaultImage,
		Entrypoint: []string{sandbox.UmpBinary, "relay", "--listen", ":" + port, "--target", target},
		// The relay needs no privileges at all, so it runs as nobody
		User: "65534:65534",
		Labels: map[string]string{
			labelInstance:    a.cfg.InstanceID,
			labelHost:        a.cfg.HostID,
			labelRole:        roleRelay,
			labelRelayTarget: target,
			labelUmpDigest:   digest,
		},
	}
	pids := int64(64)
	hostCfg := &container.HostConfig{
		NetworkMode:   "bridge",
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		CapDrop:       []string{"ALL"},
		SecurityOpt:   []string{"no-new-privileges"},
		Resources: container.Resources{
			NanoCPUs:  5e8,
			Memory:    64 * 1024 * 1024,
			PidsLimit: &pids,
		},
	}
	// host-gateway makes host.docker.internal resolve on Linux engines too; Podman adds host.containers.internal on its own
	if !a.podman {
		hostCfg.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	}

	created, err := a.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", err
	}
	if err := a.cli.CopyToContainer(ctx, created.ID, "/", bytes.NewReader(layoutArchive(ump)), container.CopyToContainerOptions{}); err != nil {
		_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("failed to inject ump into the broker relay: %w", err)
	}
	if err := a.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("failed to start the broker relay: %w", err)
	}
	a.log.InfoContext(ctx, "Started the broker relay", "container", created.ID, "target", target)
	return created.ID, nil
}

// reconnectRunNetworksLocked joins the relay to every run network of this replica
// Callers must hold a.mu
func (a *Adapter) reconnectRunNetworksLocked(ctx context.Context) {
	networks, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", labelInstance+"="+a.cfg.InstanceID),
		filters.Arg("label", labelHost+"="+a.cfg.HostID),
		filters.Arg("label", labelRole+"="+roleRunNetwork),
	)})
	if err != nil {
		a.log.WarnContext(ctx, "Failed to list sandbox networks for the new relay", "error", err)
		return
	}
	for _, n := range networks {
		if !n.Internal {
			continue
		}
		if err := a.cli.NetworkConnect(ctx, n.ID, a.relayID, &network.EndpointSettings{Aliases: []string{brokerAlias}}); err != nil {
			a.log.WarnContext(ctx, "Failed to join the relay to a sandbox network", "network", n.Name, "error", err)
		}
	}
}

// detectSelf returns the ID of the container this process runs in, or "" in binary mode
// A container the engine does not know, e.g. with a remote engine, also means binary mode
func (a *Adapter) detectSelf(ctx context.Context) string {
	if runtime.GOOS != "linux" || !runningInContainer() {
		return ""
	}
	hostname, _ := os.Hostname()
	for _, candidate := range selfCandidates(hostname) {
		info, err := a.cli.ContainerInspect(ctx, candidate)
		if err != nil || info.State == nil || !info.State.Running {
			continue
		}
		// A hostname can match an unrelated container, so require it to be that container's hostname too
		if candidate == hostname && (info.Config == nil || info.Config.Hostname != hostname) {
			continue
		}
		return info.ID
	}
	a.log.WarnContext(ctx, "Running in a container the engine does not know, falling back to the broker relay")
	return ""
}

// runningInContainer looks for the markers Docker, Podman and containerd leave
func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "docker") || strings.Contains(content, "libpod") || strings.Contains(content, "containerd")
}

// containerIDPattern matches the container ID in engine paths such as /var/lib/docker/containers/<id>/hostname
var containerIDPattern = regexp.MustCompile(`(?:containers|overlay-containers|docker|libpod)[/-]([0-9a-f]{64})`)

// selfCandidates collects possible IDs of this process's container, most reliable first
func selfCandidates(hostname string) []string {
	var candidates []string
	add := func(id string) {
		for _, c := range candidates {
			if c == id {
				return
			}
		}
		candidates = append(candidates, id)
	}

	// The engine bind-mounts /etc/hostname from a path that contains the container ID, which works on cgroup v2 too
	for _, file := range []string{"/proc/self/mountinfo", "/proc/self/cgroup"} {
		data, err := os.ReadFile(file) // #nosec G304 -- reads /proc files of this process to detect the container
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			for _, m := range containerIDPattern.FindAllStringSubmatch(scanner.Text(), -1) {
				add(m[1])
			}
		}
	}

	// Engines default the hostname to the short container ID
	if hostname != "" {
		add(hostname)
	}
	return candidates
}
