// Package sandbox defines the generic sandbox adapter interface; backend-specific code lives in subpackages such as docker (PLAN.md §4)
package sandbox

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"time"
)

// TypeDocker is the sandbox.adapter value of the Docker/Podman adapter
const TypeDocker = "docker"

// Isolation describes how strongly a sandbox is isolated from the host
type Isolation string

const (
	IsolationContainer Isolation = "container"
	IsolationGVisor    Isolation = "gvisor"
	IsolationMicroVM   Isolation = "microvm"
)

// NetworkPolicy is what a sandbox may reach besides the broker
type NetworkPolicy string

const (
	NetworkNone     NetworkPolicy = "none"
	NetworkInternet NetworkPolicy = "internet"
	// NetworkAllowlist reaches only the job's allowed domains, through the egress proxy on the broker listener
	NetworkAllowlist NetworkPolicy = "allowlist"
)

// User is a role, not a uid; adapters map it to a numeric uid so images need no passwd entries
type User string

const (
	UserAgent User = "agent"
	UserMCP   User = "mcp"
	UserRoot  User = "root"
)

// UID returns the numeric uid every adapter maps the role to
func (u User) UID() int {
	switch u {
	case UserRoot:
		return 0
	case UserMCP:
		return 1001
	default:
		return 1000
	}
}

// Well-known paths inside every sandbox
const (
	WorkspaceDir = "/workspace"
	UmpDir       = "/ump"
	UmpBinary    = "/usr/local/bin/ump"
)

// Resources are the limits of one sandbox; zero means the adapter default
type Resources struct {
	CPUs      float64
	MemoryMB  int
	PidsLimit int
	DiskMB    int
}

// BrokerAccess tells the adapter how the sandbox authenticates to the broker
// The adapter decides how the broker is reached and injects UMP_BROKER_URL and UMP_TOKEN
type BrokerAccess struct {
	// Token is the per-run broker token
	Token string
	// Port is the broker port on the replica executing the run
	Port int
}

// Spec describes the sandbox for one run
type Spec struct {
	RunID       string
	JobID       string
	WorkspaceID string
	// Image is an OCI reference resolved by the core; VM-based adapters convert it to a rootfs
	Image     string
	Resources Resources
	Network   NetworkPolicy
	// AllowPrivateNetwork lets an internet sandbox reach private ranges such as the LAN, which are blocked otherwise; metadata addresses stay blocked
	AllowPrivateNetwork bool
	// Env holds job-declared secrets only
	Env map[string]string
	// AgentUser is UserAgent by default, or UserRoot when the job opts in
	AgentUser User
	Broker    BrokerAccess
	// TTL is a hard upper bound, enforced by the backend where possible
	TTL time.Duration
}

// ExecRequest runs one command inside a sandbox
type ExecRequest struct {
	Cmd     []string
	WorkDir string
	Env     map[string]string
	User    User
	// Stdin is optional input for Exec; Attach exposes stdin through Process instead
	Stdin io.Reader
	// Stdout and Stderr receive output live; truncation and log capture are done by the caller
	Stdout io.Writer
	Stderr io.Writer
	// Timeout kills the whole process tree when exceeded; zero means no timeout besides ctx
	Timeout time.Duration
}

// ExecResult is the outcome of a finished command
type ExecResult struct {
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool
	OOMKilled bool
}

// Process is a long-lived command with interactive stdio, used for stdio MCP servers
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait blocks until the process exits
	Wait() (ExecResult, error)
	// Kill terminates the process tree
	Kill() error
}

// File is written into a sandbox by PutFiles
type File struct {
	// Path is absolute; parent directories are created as needed
	Path    string
	Mode    fs.FileMode
	Owner   User
	Content []byte
}

// Sandbox is one running sandbox
type Sandbox interface {
	ID() string
	// Exec runs a command to completion; cancelling ctx or hitting Timeout kills the process tree within 2s without destroying the sandbox
	// A timeout is reported as TimedOut, while cancelling ctx returns an error wrapping the context's error
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	// Attach starts a long-lived process with interactive stdin/stdout
	// Like exec.CommandContext, cancelling ctx or hitting Timeout kills its process tree
	Attach(ctx context.Context, req ExecRequest) (Process, error)
	// PutFiles creates or overwrites files with the given mode and owner
	// Missing parent directories are created owned by the file's owner, existing ones are left unchanged
	PutFiles(ctx context.Context, files []File) error
	// ReadFile reads a file, failing with ErrOutputTooLarge when it is larger than max bytes and with fs.ErrNotExist when it is missing
	ReadFile(ctx context.Context, path string, max int64) ([]byte, error)
	// Archive returns a tar stream of a directory with entry names relative to it, failing once more than max bytes were produced
	Archive(ctx context.Context, path string, max int64) (io.ReadCloser, error)
}

// Summary describes a sandbox owned by this instance, for the reaper
type Summary struct {
	ID        string
	RunID     string
	JobID     string
	HostID    string
	CreatedAt time.Time
}

// LimitSet reports which resource limits an adapter enforces
type LimitSet struct {
	CPU    bool
	Memory bool
	Pids   bool
	Disk   bool
}

// Capabilities are checked when a job is saved and again at run start
type Capabilities struct {
	Networks      []NetworkPolicy
	SeparateUsers bool
	Limits        LimitSet
}

// Info describes the active adapter and its backend
type Info struct {
	Adapter   string       `json:"adapter"`
	Version   string       `json:"version"`
	Arch      string       `json:"arch"`
	Isolation Isolation    `json:"isolation"`
	Caps      Capabilities `json:"capabilities"`
	// ImageBuilds reports whether the adapter implements ImageBuilder
	ImageBuilds bool `json:"imageBuilds"`
	// EgressFilter tells whether internet sandboxes are kept off private networks, and EgressFilterError why not
	EgressFilter      EgressFilter `json:"egressFilter"`
	EgressFilterError string       `json:"egressFilterError,omitempty"`
	// RuntimeError is why sandboxes can't start under the configured runtime, found by a test sandbox when the adapter was prepared
	RuntimeError string `json:"runtimeError,omitempty"`
}

// EgressFilter is the state of the rules that keep internet sandboxes off private networks (PLAN.md §4.7.3)
type EgressFilter string

const (
	EgressFilterActive      EgressFilter = "active"
	EgressFilterUnavailable EgressFilter = "unavailable"
	EgressFilterOff         EgressFilter = "off"
)

// SandboxFacing is implemented by adapters that attach this process to sandbox networks, so the server can refuse everything but the broker on those addresses
type SandboxFacing interface {
	SandboxFacing(addr netip.Addr) bool
}

// Maintainer is implemented by adapters with host-level state to repair periodically, such as firewall rules an engine restart dropped
type Maintainer interface {
	Maintain(ctx context.Context) error
}

// Adapter provisions and manages sandboxes on one isolation backend
type Adapter interface {
	Type() string
	// Check verifies the backend is reachable and reports what it supports
	Check(ctx context.Context) (Info, error)
	// Create provisions a sandbox and returns once it accepts Exec, with the ump CLI already at UmpBinary
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// Get reattaches to a sandbox created earlier, returning ErrNotFound if it is gone
	Get(ctx context.Context, id string) (Sandbox, error)
	// List returns the sandboxes owned by this instance
	List(ctx context.Context) ([]Summary, error)
	// Destroy force-removes a sandbox and everything created for it; it is idempotent
	Destroy(ctx context.Context, id string) error
	// Prepare readies the backend at startup (networks, relays, default image)
	Prepare(ctx context.Context) error
	Close() error
}

// BuildSpec describes a job image build
type BuildSpec struct {
	Dockerfile string
	// Tag is the full image reference to produce, including the registry when pushing
	Tag     string
	Push    bool
	Logs    io.Writer
	Timeout time.Duration
	// MaxSizeBytes rejects images larger than this; zero means no limit
	MaxSizeBytes int64
}

// Image is a built image
type Image struct {
	Ref       string
	Digest    string
	SizeBytes int64
}

// ImageBuilder is optionally implemented by adapters that can build job images for their own backend
type ImageBuilder interface {
	BuildImage(ctx context.Context, spec BuildSpec) (Image, error)
	// HasImage reports whether the image is present locally or can be pulled
	HasImage(ctx context.Context, ref string) (bool, error)
	// ResolveDigest resolves a reference such as debian:trixie-slim to its current repo digest, pulling it if needed
	ResolveDigest(ctx context.Context, ref string) (string, error)
	RemoveImage(ctx context.Context, ref string) error
	// ListImages returns the tags of the images this installation built on this host, so each replica can clean up its own
	ListImages(ctx context.Context) ([]string, error)
}

var (
	// ErrNotFound means the sandbox or image does not exist
	ErrNotFound = errors.New("sandbox not found")
	// ErrUnsupported means the adapter cannot provide the requested feature
	ErrUnsupported = errors.New("not supported by this sandbox adapter")
	// ErrSandboxGone means the sandbox disappeared while in use, e.g. killed for exceeding memory
	ErrSandboxGone = errors.New("sandbox is gone")
	// ErrOutputTooLarge means ReadFile or Archive exceeded its size limit
	ErrOutputTooLarge = errors.New("output exceeds the size limit")
)

// SupportsNetwork reports whether the capabilities include the policy
func (c Capabilities) SupportsNetwork(p NetworkPolicy) bool {
	for _, n := range c.Networks {
		if n == p {
			return true
		}
	}
	return false
}
