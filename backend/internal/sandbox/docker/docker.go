// Package docker implements the sandbox adapter on the Docker Engine API
//
// Design notes:
//   - Every sandbox runs the injected ump CLI as PID 1 (`ump init`), which does what `tini -- sleep infinity` would and enforces Spec.TTL, so any OCI image works, even one without tini or a shell
//   - The ump CLI and the sandbox layout are copied in between ContainerCreate and ContainerStart, so they exist before anything runs
//   - Commands run through the `ump exec` shim, because the Engine API cannot kill an exec; cancelling runs `ump exec --kill` as a second exec
//   - Each run gets an internal network ump-run-<run>, where the broker answers as umpteenth; the executing replica's own container joins it in container mode, a relay container in binary mode
//   - Internet access and allow-lists go through the broker's egress proxy on that network, so sandboxes never get a route of their own and the host firewall stays untouched
//   - Only the unrestricted network attaches the shared ump-unrestricted bridge with inter-container traffic disabled
//   - Images are built with the classic builder, since BuildKit over the Engine API needs a client session that would pull in the whole BuildKit client
package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/umpbin"
)

// Labels put on every resource the adapter creates
const (
	labelInstance    = "umpteenth.instance"
	labelHost        = "umpteenth.host"
	labelRun         = "umpteenth.run"
	labelJob         = "umpteenth.job"
	labelWorkspace   = "umpteenth.workspace"
	labelRole        = "umpteenth.role"
	labelExpires     = "umpteenth.expires"
	labelRelayTarget = "umpteenth.relay-target"
	labelUmpDigest   = "umpteenth.ump-sha256"
)

// Values of labelRole
const (
	roleSandbox      = "sandbox"
	roleRelay        = "relay"
	roleRunNetwork   = "run-network"
	roleUnrestricted = "unrestricted"
)

const (
	// brokerAlias is the name sandboxes reach the broker by on their per-run network
	brokerAlias = "umpteenth"
	// unrestrictedNetwork is the shared bridge that gives unrestricted sandboxes a route of their own; every instance on an engine uses the same one
	unrestrictedNetwork = "ump-unrestricted"
	runNetworkPrefix    = "ump-run-"
	relayNamePrefix     = "umpteenth-relay-"
	// runDir holds the exec shim's pid files; it lives outside /ump so the agent user cannot swap it out
	runDir = "/run/ump"
)

// Default limits, used for every Resources field left at zero
const (
	defaultCPUs     = 1.0
	defaultMemoryMB = 1024
	defaultRuntime  = "runc"
)

// pidsLimit caps the processes of every sandbox
const pidsLimit = 256

// gvisorDNS are the resolvers of unrestricted sandboxes under gVisor when none are configured, the same public ones Docker falls back to when the host has none
var gvisorDNS = []string{"8.8.8.8", "8.8.4.4"}

// Config configures the adapter
type Config struct {
	// InstanceID labels every resource, so two installations sharing an engine never touch each other's sandboxes
	InstanceID string
	// HostID identifies this replica; it labels the sandboxes it creates and names its relay container, so it should be stable across restarts
	HostID string
	// Runtime is the OCI runtime for sandboxes: "runc" (default) or "runsc" for gVisor, which Check verifies
	Runtime string
	// DNS are the resolvers unrestricted sandboxes use instead of Docker's embedded DNS
	// Under gVisor, which can't reach the embedded DNS, it defaults to gvisorDNS
	DNS []string
	// DefaultImage is used when a spec names no image, and runs the relay container
	DefaultImage string
	// Registry is the optional registry for job images, e.g. ghcr.io/acme/umpteenth-jobs
	Registry string
	// RegistryUsername and RegistryPassword authenticate pulls, pushes and builds against Registry's host
	RegistryUsername string
	RegistryPassword string
	// RegistryTransport validates the actual registry, redirect and token-realm connections of untrusted images
	RegistryTransport http.RoundTripper
	// BrokerPort is the port of this replica's broker listener, 8081 by default
	BrokerPort int
	// BrokerHost overrides the host the relay forwards to in binary mode (default host.docker.internal)
	BrokerHost string
	// AllowUnrestricted offers the unrestricted network, which bypasses the egress proxy
	AllowUnrestricted bool
	// Logger defaults to slog.Default()
	Logger *slog.Logger
}

// Adapter manages sandboxes as containers on one Docker engine
type Adapter struct {
	cfg     Config
	cli     *client.Client
	log     *slog.Logger
	version string
	// unrestricted names the shared bridge of unrestricted sandboxes; tests point it elsewhere so they never touch the one a running server uses
	unrestricted string
	// started separates this process's run networks from those a previous process of the same replica left behind
	started time.Time

	// mu guards the lazily prepared broker path and unrestricted network, and is held across the engine calls that set them up
	mu                  sync.Mutex
	modeDetected        bool
	selfID              string
	relayID             string
	unrestrictedCreated bool

	// stateMu guards what hot paths read, such as the server's check of every accepted connection, so it is never held across engine calls
	stateMu sync.Mutex
	// runtimeErr is why the configured custom runtime can't host sandboxes, found by the probe in Prepare
	runtimeErr error
	// sandboxFacing holds this replica's own addresses on run networks, which only the broker may be reached on
	sandboxFacing map[string]netip.Addr

	// containers caches the engine's network ranges for ContainerAddress
	containers containerRanges
}

var (
	_ sandbox.Adapter       = (*Adapter)(nil)
	_ sandbox.ImageBuilder  = (*Adapter)(nil)
	_ sandbox.Maintainer    = (*Adapter)(nil)
	_ sandbox.SandboxFacing = (*Adapter)(nil)
	_ sandbox.Containers    = (*Adapter)(nil)
)

// validName matches what the engine accepts in container and network names
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// New connects to the engine from the environment (DOCKER_HOST and friends)
func New(ctx context.Context, cfg Config) (*Adapter, error) {
	// Validate the configuration and fill in defaults
	if cfg.InstanceID == "" {
		return nil, errors.New("docker sandbox adapter: InstanceID is required")
	}
	if !validName.MatchString(cfg.HostID) {
		return nil, fmt.Errorf("docker sandbox adapter: HostID %q must be a valid container name", cfg.HostID)
	}
	if cfg.DefaultImage == "" {
		return nil, errors.New("docker sandbox adapter: DefaultImage is required")
	}
	if cfg.Runtime == "" {
		cfg.Runtime = defaultRuntime
	}
	if cfg.BrokerPort == 0 {
		cfg.BrokerPort = 8081
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create the Docker client: %w", err)
	}

	// Reach the engine up front, so a missing or unreachable one fails at startup
	v, err := cli.ServerVersion(ctx)
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("failed to reach the container engine: %w", err)
	}
	a := &Adapter{
		cfg:          cfg,
		cli:          cli,
		log:          cfg.Logger.With("scope", "sandbox", "adapter", sandbox.TypeDocker),
		version:      v.Version,
		unrestricted: unrestrictedNetwork,
		started:      time.Now(),
	}
	return a, nil
}

// Type returns the sandbox.adapter value of this adapter
func (a *Adapter) Type() string {
	return sandbox.TypeDocker
}

// Check verifies the engine, the configured runtime and the ump binary, and reports what the engine enforces
func (a *Adapter) Check(ctx context.Context) (sandbox.Info, error) {
	info, err := a.cli.Info(ctx)
	if err != nil {
		return sandbox.Info{}, fmt.Errorf("failed to query the container engine: %w", err)
	}

	arch := normalizeArch(info.Architecture)
	if arch == "" {
		return sandbox.Info{}, fmt.Errorf("unsupported engine architecture %q", info.Architecture)
	}

	// A missing runtime would otherwise only surface when the first run starts
	isolation := sandbox.IsolationContainer
	if a.customRuntime() {
		if _, ok := info.Runtimes[a.cfg.Runtime]; !ok {
			available := make([]string, 0, len(info.Runtimes))
			for name := range info.Runtimes {
				available = append(available, name)
			}
			slices.Sort(available)
			return sandbox.Info{}, fmt.Errorf("runtime %q is not configured in the container engine (available: %s)", a.cfg.Runtime, strings.Join(available, ", "))
		}
		isolation = runtimeIsolation(a.cfg.Runtime)
	}

	// Sandboxes cannot start without the ump CLI for the engine's architecture
	if _, err := umpbin.Binary(arch); err != nil {
		return sandbox.Info{}, err
	}

	return sandbox.Info{
		Adapter:   sandbox.TypeDocker,
		Version:   a.version,
		Arch:      arch,
		Isolation: isolation,
		Caps: sandbox.Capabilities{
			Networks: a.networks(),
			// Disk quotas need overlay2 on xfs with pquota, which cannot be assumed
			Limits: sandbox.LimitSet{CPU: info.CPUCfsQuota, Memory: info.MemoryLimit, Pids: info.PidsLimit},
		},
		ImageBuilds:  true,
		RuntimeError: errorText(a.runtimeProblem()),
	}, nil
}

// networks are the network policies sandboxes can have, where the unrestricted network is up to the operator
func (a *Adapter) networks() []sandbox.NetworkPolicy {
	networks := []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkInternet, sandbox.NetworkAllowlist}
	if a.cfg.AllowUnrestricted {
		networks = append(networks, sandbox.NetworkUnrestricted)
	}
	return networks
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Prepare pulls the default image, sets up the broker path and prunes networks orphaned by a crash
func (a *Adapter) Prepare(ctx context.Context) error {
	// OrbStack accepts all forwarded traffic in DOCKER-USER, which silently disables the isolation between containers on a shared bridge
	if info, err := a.cli.Info(ctx); err == nil && a.cfg.AllowUnrestricted && strings.Contains(info.OperatingSystem, "OrbStack") {
		a.log.WarnContext(ctx, "OrbStack does not enforce Docker's network isolation, so sandboxes on the unrestricted network can reach each other")
	}

	// The default image comes first, since the relay runs on it
	if _, err := a.ensureImage(ctx, a.cfg.DefaultImage); err != nil {
		return fmt.Errorf("failed to prepare the default sandbox image: %w", err)
	}
	if err := a.ensureBrokerPath(ctx); err != nil {
		return err
	}

	// Networks from before this process started are orphaned, even one a crash left mid-provisioning without a sandbox
	// Pruning also detaches this replica's container, which Docker would otherwise keep on those networks across a restart
	a.pruneOrphanNetworks(ctx, a.started)
	a.loadSandboxFacing(ctx)

	// A custom runtime is tried with a real sandbox, since a misconfigured one would only fail the first run in a confusing way
	a.checkRuntime(ctx)
	return a.runtimeProblem()
}

// orphanNetworkAge is how old a run network without a sandbox must be before maintenance removes it, far longer than creating a sandbox takes
const orphanNetworkAge = time.Hour

// Maintain prunes run networks whose sandbox is gone
func (a *Adapter) Maintain(ctx context.Context) error {
	// A sandbox removed behind the adapter's back leaves its networks, which would pile up until the engine runs out of address pools
	a.pruneOrphanNetworks(ctx, time.Now().Add(-orphanNetworkAge))
	return nil
}

// Close removes this replica's relay and releases the engine connection
// Sandboxes keep running for the reaper, but without this replica's broker the relay has nothing to forward to, and the next start recreates it
func (a *Adapter) Close() error {
	a.mu.Lock()
	relayID := a.relayID
	a.relayID = ""
	a.mu.Unlock()

	if relayID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := a.cli.ContainerRemove(ctx, relayID, container.RemoveOptions{Force: true})
		cancel()
		if err != nil && !isNotFound(err) {
			a.log.Warn("Failed to remove the broker relay", "relay", relayID, "error", err)
		}
	}
	return a.cli.Close()
}

// gvisor reports whether sandboxes run under gVisor, whose own network stack can't reach Docker's embedded DNS
func (a *Adapter) gvisor() bool {
	return runtimeIsolation(a.cfg.Runtime) == sandbox.IsolationGVisor
}

// sandboxDNS returns the resolvers unrestricted sandboxes get instead of Docker's embedded DNS, or nothing to keep it
func (a *Adapter) sandboxDNS() []string {
	if len(a.cfg.DNS) > 0 {
		return a.cfg.DNS
	}
	if a.gvisor() {
		return gvisorDNS
	}
	return nil
}

// customRuntime reports whether sandboxes need an explicit runtime instead of the engine default
func (a *Adapter) customRuntime() bool {
	return a.cfg.Runtime != "" && a.cfg.Runtime != defaultRuntime
}

// owns reports whether a resource carries this instance's label and the given role
func (a *Adapter) owns(labels map[string]string, role string) bool {
	return labels[labelInstance] == a.cfg.InstanceID && labels[labelRole] == role
}

// normalizeArch maps kernel and OCI architecture names to the names ump binaries are built for
func normalizeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

// runtimeIsolation derives the isolation level shown in the UI from the runtime name
func runtimeIsolation(runtime string) sandbox.Isolation {
	switch {
	case strings.Contains(runtime, "runsc"):
		return sandbox.IsolationGVisor
	case strings.Contains(runtime, "kata"):
		return sandbox.IsolationMicroVM
	default:
		return sandbox.IsolationContainer
	}
}

// randomID returns a random hex string of n bytes
func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// isNotFound reports whether the engine said the object does not exist
func isNotFound(err error) bool {
	return cerrdefs.IsNotFound(err)
}

// isConflict reports whether the engine refused because the object already exists or is in the wrong state
func isConflict(err error) bool {
	return cerrdefs.IsConflict(err)
}
