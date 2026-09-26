package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

const (
	// maxShadowTime bounds a shadow run's main script, which also keeps its sandbox well inside the reaper's grace period for sandboxes without a run
	maxShadowTime = 10 * time.Minute
	// shadowOutputChars is how much of main's output a shadow run keeps for reflection and the UI
	shadowOutputChars = 3_000
)

// ShadowResult is what a shadow run showed about a proposed main script
type ShadowResult struct {
	// Passed means main exited 0, reported no failure and passed every check
	Passed bool
	// Inconclusive explains why the shadow run says nothing about main either way, such as a call it had to refuse
	Inconclusive string
	// Reason explains why main didn't pass
	Reason  string
	Outputs map[string]json.RawMessage
	// Output is the end of what main printed
	Output string
	// Cost is what the script's ump llm calls and the verifier cost, in micro-USD
	Cost int64
	// Tokens is the input and output tokens of those calls
	Tokens int64
}

// Shadow runs a proposed main script once in a fresh sandbox, with the input of the run it was learned from, before it becomes the job's (PLAN.md §10.4)
// The broker refuses MCP calls that may have side effects and job state writes stay in the shadow run, but main's own network requests can't be held back, so only jobs without side effects may be shadowed
func (r *Runner) Shadow(ctx context.Context, run Run, job JobConfig) (ShadowResult, error) {
	var res ShadowResult
	if strings.TrimSpace(job.Main) == "" {
		return res, errors.New("the job has no main script")
	}

	// A real scripted run fails without a model, but that says nothing about main
	if job.ModelID == "" {
		res.Inconclusive = "no model is configured for the job"
		return res, nil
	}
	provider, model, err := r.d.Models.Resolve(ctx, run.WorkspaceID, job.ModelID)
	if err != nil {
		return res, fmt.Errorf("failed to load the model: %w", err)
	}

	// Main gets the time a real scripted run gives it, bounded so a shadow run can't hold up reflection for long
	timeout := time.Duration(job.Limits.TimeoutSeconds) * time.Second / 2
	capped := timeout > maxShadowTime
	timeout = min(timeout, maxShadowTime)
	ctx, cancel := context.WithTimeout(ctx, timeout+5*time.Minute)
	defer cancel()

	// A Dockerfile reflection changed was built before the shadow run, so the image is usually ready
	imageRef, _, err := r.d.Images.ResolveImage(ctx, job, func() {})
	if err != nil {
		return res, fmt.Errorf("failed to resolve the job image: %w", err)
	}

	// A shadow run has no run row, so only this replica's registry knows its broker token
	shadowRun := run
	shadowRun.ID = database.NewID()
	token := crypto.RandomToken(32)
	tokenHash := crypto.HashToken(token)
	agentUser := sandbox.UserAgent
	if job.RunAsRoot {
		agentUser = sandbox.UserRoot
	}
	sb, err := r.d.Adapter.Create(ctx, sandbox.Spec{
		RunID:       shadowRun.ID,
		JobID:       run.JobID,
		WorkspaceID: run.WorkspaceID,
		Image:       imageRef,
		Resources:   sandbox.Resources{CPUs: job.Limits.CPUs, MemoryMB: job.Limits.MemoryMB},
		Network:     job.Network,
		Env:         job.Env,
		AgentUser:   agentUser,
		Broker:      sandbox.BrokerAccess{Token: token, Port: r.d.BrokerPort},
		TTL:         timeout + 10*time.Minute,
	})
	if err != nil {
		return res, fmt.Errorf("failed to create the sandbox: %w", err)
	}

	// The sandbox is always destroyed, with a fresh context because the shadow run's may be over
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		err := r.d.Adapter.Destroy(destroyCtx, sb.ID())
		if err != nil {
			slog.WarnContext(destroyCtx, "Failed to destroy a shadow run's sandbox", slog.String("job", run.JobID), slog.Any("error", err))
		}
	}()

	// The sandbox may use the egress proxy from its first command on, like a real run's
	live := &LiveRun{Run: shadowRun, Job: job, Provider: provider, Model: model, Shadow: true, State: newShadowState(r.d.State.ForJob(run.WorkspaceID, run.JobID))}
	revokeProxy := r.d.Live.GrantProxy(token, runGrant(live))
	defer revokeProxy()

	// Prepare the sandbox exactly like a real run of the same input would
	err = r.injectFiles(ctx, sb, shadowRun, job)
	if err != nil {
		return res, fmt.Errorf("failed to prepare the sandbox: %w", err)
	}
	if strings.TrimSpace(job.Setup) != "" {
		exitCode, _, err := runSetup(ctx, sb, job, agentUser)
		if err != nil || exitCode != 0 {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			res.Inconclusive = fmt.Sprintf("the setup script failed with exit code %d before main could run", exitCode)
			return res, nil
		}
	}

	// The broker serves ump to main as in a real run, but reads and writes a copy of the job state
	r.attachUtilityModel(ctx, live)
	r.d.Live.RegisterShadow(tokenHash, live)
	defer r.d.Live.UnregisterShadow(tokenHash)

	// MCP servers connect as in a real run, and the broker refuses the tools that aren't read-only
	rc := RunContext{Run: shadowRun, Job: job, Sandbox: sb, Emit: func(events.Event) {}}
	var tools []agent.Tool
	for _, tp := range r.d.Tools {
		extra, cleanup, err := tp.ToolsForRun(ctx, rc)
		if err != nil {
			return res, fmt.Errorf("failed to prepare tools: %w", err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		tools = append(tools, extra...)
	}
	live.SetTools(tools)

	// Run main the way a scripted run does
	sbTools := &agent.SandboxTools{Sandbox: sb, User: agentUser, Env: job.Env, DefaultTimeout: defaultCmdTimeout, MaxTimeout: maxCmdTimeout}
	tool := sbTools.ToolkitTool(agent.ToolkitScript{ToolName: "main", Path: mainPath, Description: "The job's main script", Timeout: timeout})
	out := tool.Run(ctx, llm.ToolCall{ID: "main", Name: "main", Args: json.RawMessage("{}")})
	res.Outputs = live.Outputs()
	res.Output, _ = agent.Truncate(strings.TrimSpace(out.Content), 0, shadowOutputChars)
	spent, cost := live.FinalSpend()
	res.Tokens, res.Cost = spent.Tokens(), cost
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	// A refused call means main needs something a shadow run can't give it, which says nothing about whether it works
	if refused := live.Refused(); len(refused) > 0 {
		res.Inconclusive = "main made calls that may have side effects, which a shadow run refuses: " + strings.Join(refused, "; ")
		return res, nil
	}

	// Main only fails for being slow when it ran out of the time a real run would give it too
	if timedOut, _ := out.Meta["timedOut"].(bool); timedOut {
		if capped {
			res.Inconclusive = fmt.Sprintf("main did not finish within the %s a shadow run allows", maxShadowTime)
		} else {
			res.Reason = fmt.Sprintf("main did not finish within %s, half of the run's time limit", timeout)
		}
		return res, nil
	}

	// Judge the result with the same checks a scripted run must pass
	exitCode := -1
	if code, ok := out.Meta["exitCode"].(int); ok {
		exitCode = code
	}
	for _, c := range runChecks(ctx, sb, job, exitCode, res.Outputs, out.Content) {
		if !c.Passed {
			res.Reason = fmt.Sprintf("the check %q failed: %s", c.Check, c.Detail)
			return res, nil
		}
	}
	if failure := live.Failure(); failure != "" {
		res.Reason = "main reported a failure: " + failure
		return res, nil
	}
	if job.Verify.LLM {
		judged, usage, cost, err := judge(ctx, live, res.Outputs, out.Content)
		res.Cost += cost
		res.Tokens += usage.Tokens()
		switch {
		case err != nil:
			res.Inconclusive = "the verifier could not judge the result: " + err.Error()
			return res, nil
		case !judged.Pass:
			res.Reason = "the verifier rejected the result: " + judged.Reason
			return res, nil
		}
	}
	res.Passed = true
	return res, nil
}

// shadowState reads the job's state but keeps writes to itself, so a shadow run never changes what the next real run sees
// Its writes count against the job's state limits like a real run's would, which also bounds what it holds in the control plane's memory until it ends
type shadowState struct {
	base   agent.StateStore
	mu     sync.Mutex
	writes map[string]string
	// baseSizes holds the size of each of the job's own keys and values, read when the shadow run first writes
	baseSizes map[string]int
	// keys and size are the key count and combined size of the state the shadow run sees, its writes laid over the job's own
	keys int
	size int
}

// stateLister is a state store that can list all its entries, for ump state list
type stateLister interface {
	List(ctx context.Context) (map[string]string, error)
}

func newShadowState(base agent.StateStore) *shadowState {
	return &shadowState{base: base, writes: map[string]string{}}
}

func (s *shadowState) Get(ctx context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	v, ok := s.writes[key]
	s.mu.Unlock()
	if ok {
		return v, true, nil
	}
	return s.base.Get(ctx, key)
}

func (s *shadowState) Set(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The job's own keys count toward the limits too, since a real run's writes would land on top of them
	if s.baseSizes == nil {
		err := s.loadBase(ctx)
		if err != nil {
			return err
		}
	}

	// New keys stop at the job's limit, while replacing a value frees the size of the old one
	old, exists := s.baseSizes[key]
	if v, ok := s.writes[key]; ok {
		old, exists = len(key)+len(v), true
	}
	if !exists && s.keys >= MaxStateKeys {
		return fmt.Errorf("%w: a job keeps at most %d state keys", ErrRecordLimit, MaxStateKeys)
	}
	size := s.size - old + len(key) + len(value)
	if size > MaxStateBytes && size > s.size {
		return fmt.Errorf("%w: keys and values of a job may total at most %d MiB", ErrRecordLimit, MaxStateBytes>>20)
	}
	if !exists {
		s.keys++
	}
	s.writes[key] = value
	s.size = size
	return nil
}

// loadBase reads the size of the job's own state, which the shadow run's writes are laid over
func (s *shadowState) loadBase(ctx context.Context) error {
	sizes, total := map[string]int{}, 0
	if lister, ok := s.base.(stateLister); ok {
		base, err := lister.List(ctx)
		if err != nil {
			return err
		}
		for k, v := range base {
			sizes[k] = len(k) + len(v)
			total += sizes[k]
		}
	}
	s.baseSizes, s.keys, s.size = sizes, len(sizes), total
	return nil
}

// List merges the job's state with the shadow run's writes, for ump state list
func (s *shadowState) List(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if lister, ok := s.base.(stateLister); ok {
		base, err := lister.List(ctx)
		if err != nil {
			return nil, err
		}
		maps.Copy(out, base)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	maps.Copy(out, s.writes)
	return out, nil
}
