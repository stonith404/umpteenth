package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// reservedEnv are set by the adapter and cannot be overridden by job secrets
var reservedEnv = []string{"UMP_BROKER_URL", "UMP_TOKEN", "UMP_RUN_ID", "UMP_DNS"}

// Create provisions a sandbox container, returning once it accepts Exec
func (a *Adapter) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	// Fill in defaults and reject what this adapter cannot provide
	image := spec.Image
	if image == "" {
		image = a.cfg.DefaultImage
	}
	agentUser := spec.AgentUser
	if agentUser == "" {
		agentUser = sandbox.UserAgent
	}
	policy := spec.Network
	if policy == "" {
		policy = sandbox.NetworkInternet
	}
	if policy != sandbox.NetworkNone && policy != sandbox.NetworkInternet && policy != sandbox.NetworkAllowlist {
		return nil, fmt.Errorf("%w: network policy %q", sandbox.ErrUnsupported, policy)
	}

	// A runtime the probe found broken would only fail later with a less helpful error
	if err := a.runtimeProblem(); err != nil {
		return nil, err
	}

	// An operator who requires the egress firewall gets no internet sandbox without it
	if policy == sandbox.NetworkInternet && a.cfg.EgressFilter == EgressFilterRequired {
		if state, err := a.firewallState(ctx); state != sandbox.EgressFilterActive {
			return nil, fmt.Errorf("the egress firewall is required but not installed: %w", err)
		}
	}
	runID := spec.RunID
	if runID == "" {
		runID = randomID(8)
	}
	if !validName.MatchString(runID) {
		return nil, fmt.Errorf("run ID %q cannot be used in container names", runID)
	}

	// The image's architecture picks the ump binary, which also covers emulated foreign-arch images
	arch, err := a.imageArch(ctx, image)
	if err != nil {
		return nil, err
	}
	ump, err := a.cfg.UmpBinary(arch)
	if err != nil {
		return nil, err
	}

	// The broker port must be one the broker path can serve
	brokerPort, err := a.brokerPort(ctx, spec.Broker.Port)
	if err != nil {
		return nil, err
	}

	// Create the per-run network; from here on every step is rolled back on failure
	runNet := runNetworkPrefix + runID
	if err := a.createNetwork(ctx, runNet, true, a.runLabels(spec, runID, roleRunNetwork)); err != nil {
		return nil, err
	}
	var containerID string
	success := false
	defer func() {
		if !success {
			a.rollbackCreate(context.WithoutCancel(ctx), containerID, runID)
		}
	}()

	// Shared resources such as ump-egress or the relay can vanish between checking and starting, e.g. removed by an operator, so a missing one is recreated and the start retried once
	cfg, hostCfg := a.containerConfig(spec, image, agentUser, runID, runNet, brokerPort)
	internet := policy == sandbox.NetworkInternet
	for attempt := 0; ; attempt++ {
		if err := a.ensureShared(ctx, internet, attempt > 0); err != nil {
			return nil, err
		}
		containerID, err = a.startContainer(ctx, spec, runID, runNet, cfg, hostCfg, ump, internet)
		if err == nil {
			break
		}
		if attempt > 0 || !isMissingDependency(err) {
			return nil, err
		}
		a.log.WarnContext(ctx, "A shared sandbox resource disappeared, recreating it", "run", runID, "error", err)
	}

	success = true
	a.log.DebugContext(ctx, "Sandbox created", "sandbox", containerID, "run", runID, "image", image, "network", policy)
	return &containerSandbox{a: a, id: containerID, agentUser: agentUser}, nil
}

// ensureShared makes sure the broker path and, for internet access, the egress network exist
// With recheck set, cached knowledge about them is dropped first, because an earlier attempt found one missing
func (a *Adapter) ensureShared(ctx context.Context, internet, recheck bool) error {
	if recheck {
		a.mu.Lock()
		a.egressCreated = false
		a.mu.Unlock()
	}
	if err := a.ensureBrokerPath(ctx); err != nil {
		return err
	}
	if internet {
		return a.ensureEgress(ctx)
	}
	return nil
}

// startContainer creates, wires and starts the sandbox container, and removes it again when any step fails
func (a *Adapter) startContainer(ctx context.Context, spec sandbox.Spec, runID, runNet string, cfg *container.Config, hostCfg *container.HostConfig, ump []byte, internet bool) (string, error) {
	// The broker joins the run network first, so under gVisor its address can go into the sandbox's /etc/hosts
	// gVisor can't reach Docker's embedded DNS, while other runtimes keep resolving the alias there, which follows a replaced relay
	brokerIP, err := a.connectBroker(ctx, runNet)
	if err != nil {
		return "", err
	}
	if a.gvisor() && brokerIP.IsValid() {
		withHost := *hostCfg
		withHost.ExtraHosts = append(slices.Clone(hostCfg.ExtraHosts), brokerAlias+":"+brokerIP.String())
		hostCfg = &withHost
	}

	created, err := a.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, runNetworkPrefix+runID)
	if err != nil {
		return "", fmt.Errorf("failed to create the sandbox container: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
		}
	}()

	// Inject ump and the layout before start, because ump is the entrypoint
	if err := a.cli.CopyToContainer(ctx, created.ID, "/", bytes.NewReader(layoutArchive(ump)), container.CopyToContainerOptions{}); err != nil {
		return "", fmt.Errorf("failed to inject the ump CLI: %w", err)
	}

	// Attach internet access before the first process runs
	if internet {
		egress, err := a.egressFor(ctx, spec, runID)
		if err != nil {
			return "", err
		}
		if err := a.cli.NetworkConnect(ctx, egress, created.ID, nil); err != nil {
			return "", fmt.Errorf("failed to attach the sandbox to %s: %w", egress, err)
		}
	}

	if err := a.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start the sandbox container: %w", err)
	}
	ok = true
	return created.ID, nil
}

// isMissingDependency recognizes a network or relay container that vanished while a sandbox was being wired up
func isMissingDependency(err error) bool {
	if isNotFound(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such")
}

// Get reattaches to a running sandbox of this instance
func (a *Adapter) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	info, err := a.cli.ContainerInspect(ctx, id)
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to inspect sandbox %s: %w", id, err)
	}

	// Another instance's container is treated as absent, so instances cannot reach into each other's sandboxes
	if info.Config == nil || !a.owns(info.Config.Labels, roleSandbox) {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrNotFound, id)
	}
	if info.State == nil || !info.State.Running {
		return nil, fmt.Errorf("%w: sandbox %s is no longer running", sandbox.ErrNotFound, id)
	}

	agentUser := sandbox.User(info.Config.Labels[labelUser])
	if agentUser == "" {
		agentUser = sandbox.UserAgent
	}
	return &containerSandbox{a: a, id: info.ID, agentUser: agentUser}, nil
}

// List returns every sandbox of this instance, including stopped ones the reaper still has to remove
func (a *Adapter) List(ctx context.Context) ([]sandbox.Summary, error) {
	containers, err := a.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", labelInstance+"="+a.cfg.InstanceID),
			filters.Arg("label", labelRole+"="+roleSandbox),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	summaries := make([]sandbox.Summary, 0, len(containers))
	for _, c := range containers {
		summaries = append(summaries, sandbox.Summary{
			ID:        c.ID,
			RunID:     c.Labels[labelRun],
			JobID:     c.Labels[labelJob],
			HostID:    c.Labels[labelHost],
			CreatedAt: time.Unix(c.Created, 0),
		})
	}
	return summaries, nil
}

// Destroy force-removes a sandbox and the networks created for its run; a sandbox that is already gone is not an error
func (a *Adapter) Destroy(ctx context.Context, id string) error {
	info, err := a.cli.ContainerInspect(ctx, id)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect sandbox %s: %w", id, err)
	}

	// Never remove what another instance or the relay owns, even when handed its ID
	if info.Config == nil || !a.owns(info.Config.Labels, roleSandbox) {
		return fmt.Errorf("container %s is not a sandbox of this Umpteenth instance", id)
	}

	// The container goes first, since a network cannot be removed while it is attached
	err = a.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to remove sandbox %s: %w", id, err)
	}
	return a.removeRunNetworks(ctx, info.Config.Labels[labelRun])
}

// rollbackCreate removes whatever a failed Create left behind
func (a *Adapter) rollbackCreate(ctx context.Context, containerID, runID string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if containerID != "" {
		if err := a.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !isNotFound(err) {
			a.log.WarnContext(ctx, "Failed to remove the container of a failed sandbox", "sandbox", containerID, "error", err)
		}
	}
	if err := a.removeRunNetworks(ctx, runID); err != nil {
		a.log.WarnContext(ctx, "Failed to remove the networks of a failed sandbox", "run", runID, "error", err)
	}
}

// containerConfig builds the container and host configuration for a sandbox (PLAN.md §4.7.2)
func (a *Adapter) containerConfig(spec sandbox.Spec, image string, agentUser sandbox.User, runID, runNet string, brokerPort int) (*container.Config, *container.HostConfig) {
	labels := a.runLabels(spec, runID, roleSandbox)
	labels[labelUser] = string(agentUser)

	// ump init is PID 1 and ends the sandbox when the TTL is reached
	entrypoint := []string{sandbox.UmpBinary, "init"}
	if spec.TTL > 0 {
		entrypoint = append(entrypoint, "--ttl", spec.TTL.String())
		labels[labelExpires] = time.Now().Add(spec.TTL).UTC().Format(time.RFC3339)
	}

	cfg := &container.Config{
		Image:      image,
		Entrypoint: entrypoint,
		// Init runs as root so it can reap every user's processes; commands pick their uid per exec
		User:       "0:0",
		WorkingDir: sandbox.WorkspaceDir,
		Env:        sandboxEnv(spec, runID, brokerPort, a.sandboxDNS()),
		Labels:     labels,
	}

	// Zero means the documented default, and swap equal to memory disables swap so the limit is real
	cpus := spec.Resources.CPUs
	if cpus <= 0 {
		cpus = defaultCPUs
	}
	memoryMB := spec.Resources.MemoryMB
	if memoryMB <= 0 {
		memoryMB = defaultMemoryMB
	}
	pids := int64(spec.Resources.PidsLimit)
	if pids <= 0 {
		pids = defaultPidsLimit
	}
	memory := int64(memoryMB) * 1024 * 1024

	hostCfg := &container.HostConfig{
		NetworkMode:   container.NetworkMode(runNet),
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		SecurityOpt:   []string{"no-new-privileges"},
		Resources: container.Resources{
			NanoCPUs:   int64(cpus * 1e9),
			Memory:     memory,
			MemorySwap: memory,
			PidsLimit:  &pids,
		},
	}
	// Root keeps the engine's default capabilities because it is an explicit opt-in for package installs
	if agentUser != sandbox.UserRoot {
		hostCfg.CapDrop = []string{"ALL"}
	}
	if a.customRuntime() {
		hostCfg.Runtime = a.cfg.Runtime
	}
	return cfg, hostCfg
}

// runLabels are the labels shared by a run's container and networks
func (a *Adapter) runLabels(spec sandbox.Spec, runID, role string) map[string]string {
	return map[string]string{
		labelInstance:  a.cfg.InstanceID,
		labelHost:      a.cfg.HostID,
		labelRun:       runID,
		labelJob:       spec.JobID,
		labelWorkspace: spec.WorkspaceID,
		labelRole:      role,
	}
}

// proxyEnv are the variables that send an allow-list sandbox's traffic through the egress proxy
// Node only honors them with NODE_USE_ENV_PROXY, and both spellings are set since tools read one or the other
var proxyEnv = []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy", "NODE_USE_ENV_PROXY"}

// sandboxEnv merges the job's secrets with the broker variables, which always win
// dns replaces the resolvers of internet sandboxes, which ump init writes to /etc/resolv.conf
func sandboxEnv(spec sandbox.Spec, runID string, brokerPort int, dns []string) []string {
	allowlist := spec.Network == sandbox.NetworkAllowlist
	keys := slices.Sorted(maps.Keys(spec.Env))
	env := make([]string, 0, len(keys)+len(reservedEnv)+len(proxyEnv))
	for _, key := range keys {
		if key == "" || strings.Contains(key, "=") || slices.Contains(reservedEnv, key) || (allowlist && slices.Contains(proxyEnv, key)) {
			continue
		}
		env = append(env, key+"="+spec.Env[key])
	}
	broker := brokerAlias + ":" + strconv.Itoa(brokerPort)
	env = append(env,
		"UMP_BROKER_URL=http://"+broker,
		"UMP_TOKEN="+spec.Broker.Token,
		"UMP_RUN_ID="+runID,
	)

	// Only internet sandboxes resolve names themselves, since allow-list sandboxes leave that to the proxy
	if len(dns) > 0 && (spec.Network == "" || spec.Network == sandbox.NetworkInternet) {
		env = append(env, "UMP_DNS="+strings.Join(dns, ","))
	}

	// The egress proxy shares the broker listener and knows the run by its token
	if allowlist {
		proxy := "http://ump:" + spec.Broker.Token + "@" + broker
		noProxy := brokerAlias + ",localhost,127.0.0.1"
		env = append(env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "http_proxy="+proxy, "https_proxy="+proxy,
			"NO_PROXY="+noProxy, "no_proxy="+noProxy, "NODE_USE_ENV_PROXY=1")
	}
	return env
}

// layoutArchive is the tar extracted at / before a sandbox starts: the ump CLI, the agent-owned directories and the shim's run directory
func layoutArchive(ump []byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	agentUID := sandbox.UserAgent.UID()
	dirs := []tar.Header{
		{Name: strings.TrimPrefix(sandbox.WorkspaceDir, "/") + "/", Mode: 0o755, Uid: agentUID, Gid: agentUID},
		{Name: strings.TrimPrefix(sandbox.UmpDir, "/") + "/", Mode: 0o755, Uid: agentUID, Gid: agentUID},
		// World-writable and sticky like /tmp, since every user's shim writes its pid files here
		{Name: strings.TrimPrefix(runDir, "/") + "/", Mode: 0o1777},
	}
	for _, hdr := range dirs {
		hdr.Typeflag = tar.TypeDir
		hdr.ModTime = now
		_ = tw.WriteHeader(&hdr)
	}
	_ = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     strings.TrimPrefix(sandbox.UmpBinary, "/"),
		Mode:     0o755,
		Size:     int64(len(ump)),
		ModTime:  now,
	})
	_, _ = tw.Write(ump)
	_ = tw.Close()
	return buf.Bytes()
}
