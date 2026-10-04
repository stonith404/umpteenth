// Package sandbox defines the generic sandbox adapter interface; backend-specific code lives in subpackages such as docker
package sandbox

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"slices"
	"time"
)

// TypeDocker is the sandbox.adapter value of the Docker adapter
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
	NetworkNone NetworkPolicy = "none"
	// NetworkInternet reaches public addresses through the egress proxy on the broker listener, which keeps it off private networks
	NetworkInternet NetworkPolicy = "internet"
	// NetworkAllowlist reaches only the job's allowed domains, through the egress proxy on the broker listener
	NetworkAllowlist NetworkPolicy = "allowlist"
	// NetworkUnrestricted attaches a plain bridge that bypasses the egress proxy, so the sandbox reaches everything the host can
	NetworkUnrestricted NetworkPolicy = "unrestricted"
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
	CPUs     float64
	MemoryMB int
}

// BrokerAccess tells the adapter how the sandbox authenticates to the broker
// The adapter decides how the broker is reached and injects UMP_BROKER_URL and UMP_TOKEN
type BrokerAccess struct {
	// Token is the per-run broker token
	Token string
}

// Spec describes the sandbox for one run
type Spec struct {
	RunID       string
	JobID       string
	WorkspaceID string
	// Image is an OCI reference resolved by the core
	Image     string
	Resources Resources
	// Network decides the sandbox's topology, while which hosts a proxied sandbox reaches is up to the broker's proxy grant
	Network NetworkPolicy
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
	TimedOut  bool
	OOMKilled bool
}

// Process is a long-lived command with interactive stdio, used for stdio MCP servers
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	// Kill terminates the process tree
	Kill() error
}

// File is written into a sandbox by PutFiles
type File struct {
	// Path is absolute; parent directories are created as needed
	Path string
	// Mode defaults to 0644 and Owner to UserAgent
	Mode    fs.FileMode
	Owner   User
	Content []byte
}

// Sandbox is one running sandbox
type Sandbox interface {
	ID() string
	// Exec runs a command to completion; cancelling ctx or hitting Timeout kills the process tree within 2s without destroying the sandbox
	// A timeout is reported as TimedOut, while cancelling ctx returns an error wrapping the context's cause
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
	CreatedAt time.Time
}

// LimitSet reports which resource limits an adapter enforces
type LimitSet struct {
	CPU    bool `json:"cpu"`
	Memory bool `json:"memory"`
	Pids   bool `json:"pids"`
}

// Capabilities are checked when a job is saved and again at run start
type Capabilities struct {
	Networks []NetworkPolicy `json:"networks"`
	Limits   LimitSet        `json:"limits"`
}

// Info describes the active adapter and its backend
type Info struct {
	Adapter   string       `json:"adapter"`
	Version   string       `json:"version"`
	Arch      string       `json:"arch"`
	Isolation Isolation    `json:"isolation" enum:"container,gvisor,microvm"`
	Caps      Capabilities `json:"capabilities"`
	// ImageBuilds reports whether the adapter implements ImageBuilder
	ImageBuilds bool `json:"imageBuilds"`
	// RuntimeError is why sandboxes can't start under the configured runtime, found by a test sandbox when the adapter was prepared
	RuntimeError string `json:"runtimeError,omitempty"`
}

// SandboxFacing is implemented by adapters that attach this process to sandbox networks, so the server can refuse everything but the broker on those addresses
type SandboxFacing interface {
	SandboxFacing(addr netip.Addr) bool
}

// Containers is implemented by adapters whose containers sit on networks this process can reach, which the egress proxy must keep sandboxes off
type Containers interface {
	// ContainerAddress reports whether an address belongs to a container of the backend rather than to the host
	ContainerAddress(addr netip.Addr) bool
}

// Maintainer is implemented by adapters with host-level state to repair periodically, such as networks a crash left behind
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
	// WorkspaceID gives shared builders an immutable cache namespace
	WorkspaceID string
	// Tag is the full image reference to produce, including the registry when pushing
	Tag     string
	Push    bool
	Logs    io.Writer
	Timeout time.Duration
	// MaxSizeBytes rejects images larger than this; zero means no limit
	MaxSizeBytes int64
	// Broker holds the token of the build's proxy grant, which lets build steps reach the internet through the egress proxy; without one they get no network
	Broker BrokerAccess
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
	// ErrNotFound means the image does not exist
	ErrNotFound = errors.New("not found")
	// ErrUnsupported means the adapter cannot provide the requested feature
	ErrUnsupported = errors.New("not supported by this sandbox adapter")
	// ErrSandboxGone means the sandbox disappeared while in use, e.g. killed for exceeding memory
	ErrSandboxGone = errors.New("sandbox is gone")
	// ErrOutputTooLarge means ReadFile or Archive exceeded its size limit
	ErrOutputTooLarge = errors.New("output exceeds the size limit")
)

// SupportsNetwork reports whether the capabilities include the policy
func (c Capabilities) SupportsNetwork(p NetworkPolicy) bool {
	return slices.Contains(c.Networks, p)
}
