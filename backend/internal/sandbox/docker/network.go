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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/umpbin"
)

// ensureUnrestricted creates the shared bridge of unrestricted sandboxes once
func (a *Adapter) ensureUnrestricted(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unrestrictedCreated {
		return nil
	}

	// Disabling inter-container traffic keeps sandboxes on the shared bridge from reaching each other
	_, err := a.cli.NetworkCreate(ctx, a.unrestricted, network.CreateOptions{
		Driver:  "bridge",
		Options: map[string]string{iccOption: "false"},
		Labels:  map[string]string{labelRole: roleUnrestricted},
	})
	switch {
	case isConflict(err):
		if err := a.verifyUnrestricted(ctx, a.unrestricted); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("failed to create the %s network: %w", a.unrestricted, err)
	}
	a.unrestrictedCreated = true
	a.forgetContainerRanges()
	return nil
}

// iccOption is the bridge driver option that controls traffic between containers on the same bridge
const iccOption = "com.docker.network.bridge.enable_icc"

// verifyUnrestricted checks that an existing unrestricted bridge isolates sandboxes, since another installation or an operator may have created it
func (a *Adapter) verifyUnrestricted(ctx context.Context, name string) error {
	info, err := a.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect the %s network: %w", name, err)
	}
	if info.Driver != "bridge" || info.Options[iccOption] != "false" {
		return fmt.Errorf("the existing %s network lets sandboxes reach each other; remove it so it can be recreated", name)
	}
	return nil
}

// createNetwork creates an internal run network, accepting an existing one only if this instance created it for the same run
func (a *Adapter) createNetwork(ctx context.Context, name string, labels map[string]string) error {
	// IPv6 stays off even where the engine enables it by default, since inhibit_ipv4 only keeps the host's IPv4 address off a bridge
	opts := network.CreateOptions{Driver: "bridge", Internal: true, Labels: labels, EnableIPv6: new(false), Options: networkOptions()}
	_, err := a.cli.NetworkCreate(ctx, name, opts)
	if err == nil {
		a.forgetContainerRanges()
		return nil
	}
	if isConflict(err) {
		existing, inspectErr := a.cli.NetworkInspect(ctx, name, network.InspectOptions{})
		if inspectErr == nil && reusableNetwork(existing, labels) {
			return nil
		}
	}
	return fmt.Errorf("failed to create network %s: %w", name, err)
}

// inhibitIPv4Option is the bridge driver option that leaves the host side of a bridge without an IPv4 address
const inhibitIPv4Option = "com.docker.network.bridge.inhibit_ipv4"

// networkOptions returns the driver options createNetwork creates a network with
func networkOptions() map[string]string {
	// Docker otherwise gives the host the gateway address on the bridge, through which sandboxes would reach every service the Docker host listens on
	// Containers on the network, such as the relay or this replica's container, stay reachable without it
	return map[string]string{inhibitIPv4Option: "true"}
}

// reusableNetwork reports whether an existing network was created by this instance for the same run
func reusableNetwork(existing network.Inspect, labels map[string]string) bool {
	return existing.Labels[labelInstance] == labels[labelInstance] && existing.Labels[labelRun] == labels[labelRun]
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
	a.stateMu.Lock()
	delete(a.sandboxFacing, info.Name)
	a.stateMu.Unlock()
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
	endpoint, containerMode := a.selfID, a.selfID != ""
	if !containerMode {
		endpoint = a.relayID
	}
	a.mu.Unlock()

	err := a.cli.NetworkConnect(ctx, runNet, endpoint, &network.EndpointSettings{Aliases: []string{brokerAlias}})

	// Connecting twice fails, which is fine when the endpoint is already there, so the inspection has the last word whether or not the connect succeeded
	info, inspectErr := a.cli.NetworkInspect(ctx, runNet, network.InspectOptions{})
	joined, ok := info.Containers[endpoint]
	switch {
	case err != nil && (inspectErr != nil || !ok):
		return netip.Addr{}, fmt.Errorf("failed to connect the broker to %s: %w", runNet, err)
	case inspectErr != nil:
		return netip.Addr{}, fmt.Errorf("failed to inspect %s after connecting the broker: %w", runNet, inspectErr)
	case !ok:
		return netip.Addr{}, fmt.Errorf("the broker is missing from %s after connecting it", runNet)
	}

	// The server refuses everything but the broker on this replica's address, and gVisor needs the address in /etc/hosts, so both fail without one, while a relay under another runtime is reached by its alias
	var addr netip.Addr
	if prefix, parseErr := netip.ParsePrefix(joined.IPv4Address); parseErr == nil {
		addr = prefix.Addr()
	}
	if !addr.IsValid() && (containerMode || a.gvisor()) {
		return netip.Addr{}, fmt.Errorf("the engine reports no address of the broker on %s", runNet)
	}

	// In container mode this replica now has an address sandboxes can reach, which only the broker may answer on
	if containerMode {
		a.markSandboxFacing(runNet, addr)
	}
	return addr, nil
}

// markSandboxFacing remembers this replica's address on a run network
func (a *Adapter) markSandboxFacing(runNet string, addr netip.Addr) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.sandboxFacing == nil {
		a.sandboxFacing = map[string]netip.Addr{}
	}
	a.sandboxFacing[runNet] = addr
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
		if !strings.HasPrefix(name, runNetworkPrefix) || ep == nil {
			continue
		}
		if addr, err := netip.ParseAddr(ep.IPAddress); err == nil {
			a.markSandboxFacing(name, addr)
		}
	}
}

// SandboxFacing reports whether an address is one of this replica's addresses on a run network
// The server refuses API and other non-broker connections on them, so a sandbox reaches nothing but the broker
func (a *Adapter) SandboxFacing(addr netip.Addr) bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
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
	ump, err := umpbin.Binary(arch)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(ump)
	digest := hex.EncodeToString(sum[:])

	// Reuse a relay that matches, restart a stopped one, and replace an outdated one
	info, err := a.cli.ContainerInspect(ctx, name)
	switch {
	case err == nil && info.Config != nil && info.Config.Image == a.imageRef(a.cfg.DefaultImage) &&
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
		Image:      a.imageRef(a.cfg.DefaultImage),
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
	hostCfg := &container.HostConfig{
		NetworkMode:   "bridge",
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		CapDrop:       []string{"ALL"},
		SecurityOpt:   []string{"no-new-privileges"},
		NanoCPUs:      5e8,
		Memory:        64 * 1024 * 1024,
		PidsLimit:     new(int64(64)),
		// host-gateway makes host.docker.internal resolve on Linux engines too
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}

	created, err := a.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", err
	}
	if err := a.cli.CopyToContainer(ctx, created.ID, "/", bytes.NewReader(layoutArchive(ump, false)), container.CopyToContainerOptions{}); err != nil {
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

// runningInContainer looks for the markers Docker, containerd and Podman leave
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
	return strings.Contains(content, "docker") || strings.Contains(content, "containerd") || strings.Contains(content, "libpod")
}

// containerIDPattern matches the container ID in engine paths such as /var/lib/docker/containers/<id>/hostname or Podman's libpod-<id>.scope
var containerIDPattern = regexp.MustCompile(`(?:containers|docker|libpod)[/-]([0-9a-f]{64})`)

// selfCandidates collects possible IDs of this process's container, most reliable first
func selfCandidates(hostname string) []string {
	var candidates []string
	add := func(id string) {
		if !slices.Contains(candidates, id) {
			candidates = append(candidates, id)
		}
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
