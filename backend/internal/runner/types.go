// Package runner executes one run: sandbox lifecycle, agent loop, events and results
// It depends only on small interfaces, so it knows nothing about the modules that feed it
package runner

import (
	"context"
	"encoding/json"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// Run statuses
const (
	StatusQueued       = "queued"
	StatusProvisioning = "provisioning"
	StatusRunning      = "running"
	StatusVerifying    = "verifying"
	StatusSucceeded    = "succeeded"
	StatusFailed       = "failed"
	StatusCancelled    = "cancelled"
	StatusTimedOut     = "timed_out"
	StatusSkipped      = "skipped"
)

// Run modes
const (
	ModeExplore  = "explore"
	ModeAssisted = "assisted"
	ModeScripted = "scripted"
)

// IsTerminal reports whether a run status is final
func IsTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut, StatusSkipped:
		return true
	}
	return false
}

// Run is the run row as the runner needs it
type Run struct {
	ID              string
	WorkspaceID     string
	JobID           string
	Number          int64
	Status          string
	Mode            string
	Trigger         string
	Input           json.RawMessage
	Instructions    string
	PlaybookVersion int64
}

// Limits are the effective limits of a run
type Limits struct {
	TimeoutSeconds int
	MaxTurns       int
	// MaxCost is in micro-USD
	MaxCost  int64
	CPUs     float64
	MemoryMB int
}

// ToolkitScript is one playbook script, injected into /ump/toolkit and offered to the agent as a tool
type ToolkitScript struct {
	Name        string
	Content     string
	Description string
	// Args maps argument names to their ump:args types
	Args        map[string]string
	SideEffects bool
}

// ToolkitUsage counts how one toolkit script did in a run
type ToolkitUsage struct {
	Calls    int
	Failures int
}

// JobConfig is everything about the job a run needs, resolved by the jobs module
type JobConfig struct {
	ID              string
	WorkspaceID     string
	Name            string
	Instruction     string
	SuccessCriteria []string
	Outputs         []string
	// OutputNames are the names of the outputs the job defines, which a scripted run must report
	OutputNames []string
	// Inputs describe the inputs the job declares, and InputNames are their names, which a main script reads from /ump/input.json
	Inputs     []string
	InputNames []string
	// SideEffects lists what the job changes outside the sandbox, which a shadow run must never repeat
	SideEffects []string
	// ModelID is the job's model or the workspace default, empty when none is configured
	ModelID   string
	BaseImage string
	Network   sandbox.NetworkPolicy
	// AllowedDomains are what an allow-list job may reach, and AllowPrivateNetwork lets a job reach private ranges such as the LAN
	AllowedDomains      []string
	AllowPrivateNetwork bool
	RunAsRoot           bool
	// SelfImprove turns on reflection after every run
	SelfImprove bool
	// Graduate lets reflection turn the job into a main script that runs without the agent
	Graduate bool
	Limits   Limits
	Timezone string
	// DailySpendLimit is the workspace cap in micro-USD, zero means none
	DailySpendLimit int64
	// Env holds the job's secrets mapped to environment variable names
	Env map[string]string

	PlaybookVersion  int64
	PlaybookRendered string
	PlaybookMarkdown string
	Setup            string
	// Main and Verify make the job scripted once it graduated
	Main           string
	Verify         playbook.Verify
	Dockerfile     string
	DockerfileHash string
	Toolkit        []ToolkitScript
}

// Model is a resolved model
type Model struct {
	ID    string
	Name  string
	Price llm.Price
	Caps  llm.Caps
}

// SandboxRecord is stored on the run once the sandbox exists
type SandboxRecord struct {
	Adapter     string
	SandboxID   string
	Isolation   string
	ImageRef    string
	ImageID     string
	ModelID     string
	MsProvision int64
}

// Final is the result written to the run row
type Final struct {
	Status     string
	Summary    string
	Outputs    json.RawMessage
	Error      string
	Usage      llm.Usage
	Cost       int64
	Turns      int
	MsLLM      int64
	MsTools    int64
	MsTotal    int64
	Reflection string
	Notes      []string
	// Toolkit counts the toolkit tool calls of the run by script name
	Toolkit map[string]ToolkitUsage
	// FellBack is set when the run's main script failed and an agent finished the job
	FellBack bool
	// MainRan is set once a scripted run started its main script, which makes a run without agent turns worth learning from
	MainRan bool
	// SetupFailed is set when the playbook's setup script exited non-zero, which reflection can repair
	SetupFailed bool
	// VerifyCost is what the utility model's verification of a scripted run cost, in micro-USD
	VerifyCost int64
	// VerifyTokens is the input and output tokens of that verification
	VerifyTokens int64
	// SelfImprove tells whether the job learns from its runs automatically
	SelfImprove bool
}

// Reflection states of a run
const (
	ReflectionSkipped = "skipped"
	ReflectionPending = "pending"
)

// ReflectionWanted decides whether a finished run is worth learning from
func ReflectionWanted(run Run, final Final) bool {
	if !final.SelfImprove {
		return false
	}
	switch final.Status {
	case StatusSucceeded, StatusFailed, StatusTimedOut:
	default:
		return false
	}

	// A run that ended before the agent or its main script started has nothing to learn from, unless the playbook's own setup script broke it
	if final.Turns == 0 && !final.FellBack && !final.MainRan && !final.SetupFailed {
		return false
	}

	// A successful scripted run costs nothing extra, since reflection has nothing to add, while a fallback means main needs repair
	return run.Mode != ModeScripted || final.FellBack || final.Status != StatusSucceeded || len(final.Notes) > 0
}

// RunStore reads and updates run rows
type RunStore interface {
	Load(ctx context.Context, runID string) (Run, error)
	// Claim moves a queued run to provisioning on this replica; it returns false when the run is not queued anymore
	Claim(ctx context.Context, runID, hostID string) (bool, error)
	SetStatus(ctx context.Context, runID, status string) error
	SetSandbox(ctx context.Context, runID string, rec SandboxRecord) error
	SetBrokerToken(ctx context.Context, runID, tokenHash string) error
	// SetFellBack records that the run's main script failed and an agent took over
	SetFellBack(ctx context.Context, runID string) error
	// Heartbeat records that the run is alive and reports whether a cancel was requested
	Heartbeat(ctx context.Context, runID string) (bool, error)
	// Finish writes the final result; it returns false when the run already had a final status
	Finish(ctx context.Context, runID string, final Final) (bool, error)
	SpentSince(ctx context.Context, workspaceID string, since int64) (int64, error)
}

// JobLoader resolves the job configuration for a run
type JobLoader interface {
	LoadForRun(ctx context.Context, workspaceID, jobID string, playbookVersion int64) (JobConfig, error)
}

// ModelResolver turns a model ID into a ready provider
type ModelResolver interface {
	Resolve(ctx context.Context, workspaceID, modelID string) (llm.Provider, Model, error)
}

// StateStores gives the agent access to one job's persistent state
type StateStores interface {
	ForJob(workspaceID, jobID string) agent.StateStore
}

// Limits of one job's persistent state, which the job's store and a shadow run's copy of it both enforce
const (
	// MaxStateKeys bounds how many keys one job's state can hold
	MaxStateKeys = 1000
	// MaxStateBytes bounds the combined size of a job's keys and values, since ump state list reads all of them into the control plane's memory at once
	MaxStateBytes = 16 << 20
)

// ImageResolver returns the image for a run, waiting for a build of the job's Dockerfile if needed
type ImageResolver interface {
	ResolveImage(ctx context.Context, job JobConfig, onWait func()) (ref string, imageID string, err error)
}

// ToolProvider adds tools for a run, such as the job's MCP servers, and cleans them up afterwards
type ToolProvider interface {
	ToolsForRun(ctx context.Context, rc RunContext) ([]agent.Tool, func(), error)
}

// RunContext is what a ToolProvider or broker needs to know about a live run
type RunContext struct {
	Run     Run
	Job     JobConfig
	Sandbox sandbox.Sandbox
	Emit    func(e events.Event)
}

// Notifier is told about finished runs, so the job actor can release its concurrency slot
type Notifier interface {
	RunFinished(ctx context.Context, run Run, final Final)
}

// CancelWaiter blocks until a cancel is requested for the run, from any replica
type CancelWaiter interface {
	WaitCancel(ctx context.Context, runID string) error
}
