package runner

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"path"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

const (
	heartbeatInterval  = 15 * time.Second
	maxArtifactsBytes  = 50 << 20
	maxCandidatesBytes = 1 << 20
	defaultCmdTimeout  = 120 * time.Second
	maxCmdTimeout      = 600 * time.Second
	// collectTimeout bounds collecting a finished run's files, since the run keeps heartbeating meanwhile and a hung sandbox must not hold it open forever
	collectTimeout = 2 * time.Minute
)

// Deps are the collaborators of the runner, all of which are required
type Deps struct {
	DB         *database.DB
	Runs       RunStore
	Jobs       JobLoader
	Models     ModelResolver
	State      StateStores
	Adapter    sandbox.Adapter
	Images     ImageResolver
	Tools      []ToolProvider
	Notifier   Notifier
	Cancel     CancelWaiter
	Live       *Registry
	Bus        events.Bus
	Storage    storage.FileStorage
	HostID     string
	BrokerPort int
	// UtilityModel resolves the workspace utility model for ump llm, returning an empty ID when none is set
	UtilityModel func(ctx context.Context, workspaceID string) (string, error)
}

// Runner executes runs delivered by the runs taskpool
type Runner struct {
	d Deps
}

func New(d Deps) *Runner {
	return &Runner{d: d}
}

// Execute runs one run to completion
// It is safe against redelivery: only a queued run is ever executed, and a run found mid-flight is failed, never re-executed (PLAN.md §3.3)
func (r *Runner) Execute(ctx context.Context, runID string) error {
	run, err := r.d.Runs.Load(ctx, runID)
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to load run: %w", err)
	}

	rec := events.NewRecorder(r.d.DB, r.d.Bus, r.d.Storage, run.ID)
	switch {
	case run.Status == StatusQueued:
	case IsTerminal(run.Status):
		return nil
	default:
		// A redelivered task for a run that was already in flight means its replica died: fail it rather than repeat side effects
		final := Final{Status: StatusFailed, Error: "interrupted: the replica executing this run stopped"}
		rec.Emit(ctx, events.Event{Type: events.TypeRunStatus, Payload: map[string]any{"status": final.Status, "error": final.Error}})
		r.finish(ctx, run, final)
		return nil
	}

	claimed, err := r.d.Runs.Claim(ctx, runID, r.d.HostID)
	if err != nil {
		return fmt.Errorf("failed to claim run: %w", err)
	}
	if !claimed {
		return nil
	}

	started := time.Now()
	r.publishStatus(ctx, run, StatusProvisioning)

	// A cancel can arrive through the heartbeat flag or a Francis signal, whichever replica received the request
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// Heartbeats outlive a cancel and last until the result is recorded, since collecting outputs and destroying the sandbox still take time and a silent run gets failed as interrupted
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go r.heartbeat(heartbeatCtx, run.ID, cancel)
	go func() {
		if err := r.d.Cancel.WaitCancel(runCtx, run.ID); err == nil {
			cancel(errCancelled)
		}
	}()

	// A requested cancel ends a run that didn't succeed as cancelled, whatever failed on its way out
	final := r.execute(runCtx, run, rec)
	if errors.Is(context.Cause(runCtx), errCancelled) && final.Status != StatusSucceeded {
		final.Status, final.Error = StatusCancelled, "Cancelled"
	}
	final.MsTotal = time.Since(started).Milliseconds()

	// Whether reflection follows is stored with the final status, so nobody sees a finished run that looks like it won't be learned from
	if ReflectionWanted(run, final) {
		final.Reflection = ReflectionPending
	}

	rec.Emit(ctx, events.Event{Type: events.TypeRunStatus, Payload: map[string]any{"status": final.Status, "error": final.Error}})
	r.finish(ctx, run, final)
	return nil
}

// errCancelled is the cause of a run's context when someone asked to cancel the run
var errCancelled = errors.New("run cancelled")

// stopped is the result of a run whose context ended before the run did
func stopped(ctx context.Context) Final {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Final{Status: StatusTimedOut, Error: "the run exceeded its time limit"}
	}
	return Final{Status: StatusCancelled, Error: "run was stopped"}
}

// execute does the actual work and always returns a final result
func (r *Runner) execute(ctx context.Context, run Run, rec *events.Recorder) Final {
	// The job is loaded below, and a failure after that still knows whether the job learns from it
	var job JobConfig
	fail := func(format string, args ...any) Final {
		final := Final{Status: StatusFailed, Error: fmt.Sprintf(format, args...), SelfImprove: job.SelfImprove}

		// A step that failed because the run was stopped or ran out of time ends the run that way, before it did anything to learn from
		if ctx.Err() != nil {
			final = stopped(ctx)
		}
		rec.Emit(ctx, events.Event{Type: events.TypeError, Payload: map[string]any{"message": final.Error}})
		return final
	}

	provisionStart := time.Now()
	rec.Emit(ctx, events.Event{Type: events.TypeRunStatus, Payload: map[string]any{"status": StatusProvisioning}})

	// Resolve the job, its limits and its model
	job, err := r.d.Jobs.LoadForRun(ctx, run.WorkspaceID, run.JobID, run.PlaybookVersion)
	if err != nil {
		return fail("Failed to load the job: %v", err)
	}
	if job.ModelID == "" {
		return fail("No model is configured. Pick a model for the job or set a default agent model in Settings.")
	}
	provider, model, err := r.d.Models.Resolve(ctx, run.WorkspaceID, job.ModelID)
	if err != nil {
		return fail("Failed to load the model: %v", err)
	}

	// The workspace daily spend cap is checked before any sandbox or model cost is incurred
	if job.DailySpendLimit > 0 {
		spent, err := r.d.Runs.SpentSince(ctx, run.WorkspaceID, startOfDay(time.Now()))
		if err != nil {
			return fail("Failed to check the daily spend limit: %v", err)
		}
		if spent >= job.DailySpendLimit {
			return fail("The workspace reached its daily spend limit of $%.2f", float64(job.DailySpendLimit)/1e6)
		}
	}

	// The whole run has a wall-clock limit, which also bounds the time spent waiting for an image
	deadline := time.Now().Add(time.Duration(job.Limits.TimeoutSeconds) * time.Second)
	ctx, cancelDeadline := context.WithDeadline(ctx, deadline)
	defer cancelDeadline()

	// Resolve the image, waiting for a Dockerfile build when the job has one
	waitSpan := events.StartSpan("")
	waited := false
	imageRef, imageID, err := r.d.Images.ResolveImage(ctx, job, func() {
		waited = true
		rec.Emit(ctx, events.Event{Type: events.TypeImageWait, SpanID: waitSpan.ID, Payload: map[string]any{"message": "Waiting for the job image to build"}})
	})
	if waited {
		rec.Emit(ctx, events.Event{Type: events.TypeImageWait, SpanID: waitSpan.ID, Ms: waitSpan.Elapsed(), Payload: map[string]any{"done": err == nil}})
	}
	if err != nil {
		return fail("Environment build failed: %v", err)
	}

	// Each run gets a broker token that is only valid while the run is live
	brokerToken := crypto.RandomToken(32)
	err = r.d.Runs.SetBrokerToken(ctx, run.ID, crypto.HashToken(brokerToken))
	if err != nil {
		return fail("Failed to create the broker token: %v", err)
	}

	// Create the sandbox
	agentUser := sandbox.UserAgent
	if job.RunAsRoot {
		agentUser = sandbox.UserRoot
	}
	createSpan := events.StartSpan("")
	sb, err := r.d.Adapter.Create(ctx, sandbox.Spec{
		RunID:       run.ID,
		JobID:       run.JobID,
		WorkspaceID: run.WorkspaceID,
		Image:       imageRef,
		Resources:   sandbox.Resources{CPUs: job.Limits.CPUs, MemoryMB: job.Limits.MemoryMB},
		Network:     job.Network,
		Env:         job.Env,
		AgentUser:   agentUser,
		Broker:      sandbox.BrokerAccess{Token: brokerToken, Port: r.d.BrokerPort},
		TTL:         time.Duration(job.Limits.TimeoutSeconds)*time.Second + 5*time.Minute,
	})
	if err != nil {
		return fail("Failed to create the sandbox: %v", err)
	}

	// The sandbox is always destroyed, with a fresh context because the run's may be canceled
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		span := events.StartSpan("")
		err := r.d.Adapter.Destroy(destroyCtx, sb.ID())
		if err != nil {
			slog.WarnContext(destroyCtx, "Failed to destroy sandbox", slog.String("run", run.ID), slog.Any("error", err))
		}
		rec.Emit(destroyCtx, events.Event{Type: events.TypeSandboxDestroy, SpanID: span.ID, Ms: span.Elapsed(), Payload: map[string]any{"sandboxId": sb.ID()}})
	}()

	info, _ := r.d.Adapter.Check(ctx)
	rec.Emit(ctx, events.Event{Type: events.TypeSandboxCreate, SpanID: createSpan.ID, Ms: createSpan.Elapsed(), Payload: map[string]any{
		"sandboxId": sb.ID(), "image": imageRef, "adapter": r.d.Adapter.Type(), "isolation": info.Isolation,
	}})
	err = r.d.Runs.SetSandbox(ctx, run.ID, SandboxRecord{
		Adapter: r.d.Adapter.Type(), SandboxID: sb.ID(), Isolation: string(info.Isolation),
		ImageRef: imageRef, ImageID: imageID, ModelID: model.ID, MsProvision: time.Since(provisionStart).Milliseconds(),
	})
	if err != nil {
		slog.WarnContext(ctx, "Failed to record the run's sandbox", slog.String("run", run.ID), slog.Any("error", err))
	}

	// The sandbox may use the egress proxy from its first command on, the setup script included
	live := &LiveRun{Run: run, Job: job, Recorder: rec, Provider: provider, Model: model}
	revokeProxy := r.d.Live.GrantProxy(brokerToken, runGrant(live))
	defer revokeProxy()

	// Inject the run input, the playbook and the toolkit
	err = r.injectFiles(ctx, sb, run, job)
	if err != nil {
		return fail("Failed to prepare the sandbox: %v", err)
	}

	// Run the playbook setup script, for cheap per-run preparation
	if strings.TrimSpace(job.Setup) != "" {
		span := events.StartSpan("")
		exitCode, setupOut, err := runSetup(ctx, sb, job, agentUser)
		rec.Emit(ctx, events.Event{Type: events.TypeSandboxSetup, SpanID: span.ID, Ms: span.Elapsed(), Payload: map[string]any{"exitCode": exitCode, "output": setupOut}})
		if err != nil {
			return fail("The setup script could not run: %v", err)
		}
		if exitCode != 0 {
			final := fail("The setup script failed with exit code %d", exitCode)
			final.SetupFailed = true
			return final
		}
	}

	// Make the run reachable for the broker on this replica
	r.attachUtilityModel(ctx, live)
	r.d.Live.Register(run.ID, live)
	defer r.d.Live.Unregister(run.ID)

	// Collect the tools: built-ins, job state, and anything providers such as MCP add
	notes := &agent.Notes{}
	obs := newTimelineObserver(ctx, rec, "")
	sbTools := &agent.SandboxTools{Sandbox: sb, User: agentUser, Env: job.Env, DefaultTimeout: defaultCmdTimeout, MaxTimeout: maxCmdTimeout, Sink: obs.ToolOutput}
	tools := append(sbTools.Tools(), agent.StateTools(r.d.State.ForJob(run.WorkspaceID, run.JobID))...)
	tools = append(tools, agent.RememberTool(notes), agent.FinishTool())
	usage := &toolkitCounter{}
	tools = append(tools, toolkitTools(ctx, run, job, sbTools, usage)...)

	rc := RunContext{Run: run, Job: job, Sandbox: sb, Emit: func(e events.Event) { rec.Emit(ctx, e) }}
	var brokerTools []agent.Tool
	for _, tp := range r.d.Tools {
		extra, cleanup, err := tp.ToolsForRun(ctx, rc)
		if err != nil {
			return fail("Failed to prepare tools: %v", err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		tools = append(tools, extra...)
		brokerTools = append(brokerTools, extra...)
	}
	live.SetTools(brokerTools)

	err = r.d.Runs.SetStatus(ctx, run.ID, StatusRunning)
	if err != nil {
		return fail("Failed to start the run: %v", err)
	}
	r.publishStatus(ctx, run, StatusRunning)
	rec.Emit(ctx, events.Event{Type: events.TypeRunStatus, Payload: map[string]any{"status": StatusRunning}})

	// A graduated job runs its main script and only calls on the agent when the script fails (PLAN.md §10.4)
	sess := &session{
		run: run, job: job, sb: sb, rec: rec, live: live, obs: obs, sbTools: sbTools,
		tools: tools, notes: notes, usage: usage, provider: provider, model: model, deadline: deadline,
	}
	var final Final
	if run.Mode == ModeScripted && strings.TrimSpace(job.Main) != "" {
		final = r.runScripted(ctx, sess)
		final.MainRan = true
	} else {
		final = r.runAgent(ctx, sess, "")
	}

	// Collect artifacts even from stopped or failed runs, since partial outputs help debugging
	// Candidate scripts are kept for reflection, which runs after the sandbox is gone
	collectCtx, cancelCollect := context.WithTimeout(context.WithoutCancel(ctx), collectTimeout)
	defer cancelCollect()
	r.collectArtifacts(collectCtx, sb, run, rec)
	r.collectCandidates(collectCtx, sb, run)
	final.SelfImprove = job.SelfImprove
	return final
}

// session is everything one executing run has set up, shared by the scripted and the agent path
type session struct {
	run      Run
	job      JobConfig
	sb       sandbox.Sandbox
	rec      *events.Recorder
	live     *LiveRun
	obs      *timelineObserver
	sbTools  *agent.SandboxTools
	tools    []agent.Tool
	notes    *agent.Notes
	usage    *toolkitCounter
	provider llm.Provider
	model    Model
	deadline time.Time
}

// runAgent drives the agent loop, starting from a resume brief when it takes over from a failed main script
func (r *Runner) runAgent(ctx context.Context, s *session, resume string) Final {
	outcome := agent.Run(ctx, agent.Config{
		Provider: s.provider,
		Model:    s.model.Name,
		Price:    s.model.Price,
		System: agent.SystemBlocks(agent.PromptInput{
			Instruction:     s.job.Instruction,
			SuccessCriteria: s.job.SuccessCriteria,
			Outputs:         s.job.Outputs,
			Playbook:        s.job.PlaybookRendered,
		}),
		Tools: s.tools,
		Budget: agent.Budget{MaxTurns: s.job.Limits.MaxTurns, MaxCost: s.job.Limits.MaxCost, Deadline: s.deadline, ExtraCost: func() int64 {
			_, cost := s.live.Spend()
			return cost
		}},
		Effort:        llm.EffortMedium,
		Observer:      costObserver{Observer: s.obs, live: s.live},
		ContextWindow: s.model.Caps.Context,
	}, []llm.Message{agent.RunContext{
		Trigger:      s.run.Trigger,
		Time:         time.Now(),
		Timezone:     s.job.Timezone,
		Input:        s.run.Input,
		Instructions: s.run.Instructions,
		Resume:       resume,
	}.UserMessage()})

	final := Final{
		Status:  string(outcome.Status),
		Error:   outcome.Reason,
		Usage:   outcome.Usage,
		Cost:    outcome.Cost,
		Turns:   outcome.Turns,
		MsLLM:   outcome.LLMTime.Milliseconds(),
		MsTools: outcome.ToolTime.Milliseconds(),
		Notes:   s.notes.All(),
		Toolkit: s.usage.all(),
	}

	// Broker LLM calls from ump llm count towards the run's cost
	brokerUsage, brokerCost := s.live.FinalSpend()
	final.Usage = final.Usage.Add(brokerUsage)
	final.Cost += brokerCost

	outputs := map[string]any{}
	for k, v := range s.live.Outputs() {
		outputs[k] = v
	}
	if outcome.Finish != nil {
		final.Summary = outcome.Finish.Summary
		maps.Copy(outputs, outcome.Finish.Outputs)
		s.rec.Emit(ctx, events.Event{Type: events.TypeFinish, Payload: outcome.Finish})
	}
	if len(outputs) > 0 {
		final.Outputs, _ = json.Marshal(outputs)
	}
	if reason := s.live.Failure(); reason != "" && final.Status == StatusSucceeded {
		final.Status, final.Error = StatusFailed, reason
	}
	if final.Status != StatusSucceeded && final.Error != "" {
		s.rec.Emit(ctx, events.Event{Type: events.TypeError, Payload: map[string]any{"message": final.Error}})
	}
	return final
}

// injectFiles writes the run input, the playbook and the toolkit into the sandbox
func (r *Runner) injectFiles(ctx context.Context, sb sandbox.Sandbox, run Run, job JobConfig) error {
	input := run.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	files := []sandbox.File{
		{Path: "/ump/input.json", Mode: 0o644, Owner: sandbox.UserAgent, Content: input},
		{Path: "/ump/PLAYBOOK.md", Mode: 0o644, Owner: sandbox.UserAgent, Content: []byte(job.PlaybookMarkdown)},
		{Path: "/ump/outputs/.keep", Mode: 0o644, Owner: sandbox.UserAgent},
		{Path: "/ump/candidates/.keep", Mode: 0o644, Owner: sandbox.UserAgent},
		{Path: "/ump/logs/.keep", Mode: 0o644, Owner: sandbox.UserAgent},
	}
	// A graduated job's main script, which the agent can also read when it takes over from it
	if strings.TrimSpace(job.Main) != "" {
		files = append(files, sandbox.File{Path: mainPath, Mode: 0o755, Owner: sandbox.UserAgent, Content: []byte(job.Main)})
	}
	for _, s := range job.Toolkit {
		// Names are validated when a playbook is saved, but a name that could escape /ump/toolkit must never reach the sandbox
		if !playbook.ValidScriptName(s.Name) {
			slog.WarnContext(ctx, "Skipping a toolkit script with an invalid name", slog.String("run", run.ID), slog.String("name", s.Name))
			continue
		}
		files = append(files, sandbox.File{Path: path.Join("/ump/toolkit", s.Name), Mode: 0o755, Owner: sandbox.UserAgent, Content: []byte(s.Content)})
	}
	return sb.PutFiles(ctx, files)
}

// runSetup runs the playbook's setup script and returns its exit code and the ends of its output
func runSetup(ctx context.Context, sb sandbox.Sandbox, job JobConfig, user sandbox.User) (int, string, error) {
	// Only the ends of the output are kept, since a setup script can print without end for its whole timeout
	out := agent.NewHeadTail(4000, 4000)
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"bash", "-lc", job.Setup}, WorkDir: sandbox.WorkspaceDir, User: user, Env: job.Env, Stdout: out, Stderr: out, Timeout: 10 * time.Minute})
	output, _ := out.String()
	return res.ExitCode, output, err
}

// collectArtifacts copies /ump/outputs into FileStorage
func (r *Runner) collectArtifacts(ctx context.Context, sb sandbox.Sandbox, run Run, rec *events.Recorder) {
	collected, err := r.collectDir(ctx, sb, "/ump/outputs", "runs/"+run.ID+"/artifacts/", maxArtifactsBytes)
	if len(collected) > 0 {
		rec.Emit(ctx, events.Event{Type: events.TypeLog, Payload: map[string]any{"message": fmt.Sprintf("Collected %d artifact(s)", len(collected)), "artifacts": collected}})
	}
	if err != nil {
		rec.Emit(ctx, events.Event{Type: events.TypeLog, Payload: map[string]any{"message": "Could not collect every artifact: " + err.Error()}})
	}
}

// collectCandidates keeps the scripts the agent wrote under /ump/candidates, which reflection may promote to the toolkit
func (r *Runner) collectCandidates(ctx context.Context, sb sandbox.Sandbox, run Run) {
	_, err := r.collectDir(ctx, sb, "/ump/candidates", "runs/"+run.ID+"/candidates/", maxCandidatesBytes)
	if err != nil {
		slog.WarnContext(ctx, "Failed to collect candidate scripts", slog.String("run", run.ID), slog.Any("error", err))
	}
}

// collectDir copies the regular files of a sandbox directory into FileStorage under a prefix, returning the files it stored before any error
func (r *Runner) collectDir(ctx context.Context, sb sandbox.Sandbox, dir, prefix string, limit int64) ([]string, error) {
	archive, err := sb.Archive(ctx, dir, limit)
	if err != nil {
		return nil, err
	}
	defer archive.Close()

	// The archive fails once it grows past the limit, which bounds every file in it too
	tr := tar.NewReader(archive)
	var collected []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return collected, nil
		}
		if err != nil {
			return collected, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Entry names are relative to the directory, and cleaning them keeps every stored name under the prefix
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if name == ".keep" || name == "" {
			continue
		}
		err = r.d.Storage.Save(ctx, prefix+name, tr)
		if err != nil {
			return collected, fmt.Errorf("failed to store %s: %w", name, err)
		}
		collected = append(collected, name)
	}
}

// attachUtilityModel resolves the workspace utility model for ump llm, falling back to the run's model
func (r *Runner) attachUtilityModel(ctx context.Context, live *LiveRun) {
	live.Utility, live.UtilModel = live.Provider, live.Model
	id, err := r.d.UtilityModel(ctx, live.Run.WorkspaceID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to load the workspace utility model", slog.String("run", live.Run.ID), slog.Any("error", err))
		return
	}
	if id == "" {
		return
	}
	p, m, err := r.d.Models.Resolve(ctx, live.Run.WorkspaceID, id)
	if err != nil {
		slog.WarnContext(ctx, "Failed to resolve the workspace utility model", slog.String("run", live.Run.ID), slog.String("model", id), slog.Any("error", err))
		return
	}
	live.Utility, live.UtilModel = p, m
}

func (r *Runner) heartbeat(ctx context.Context, runID string, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cancelRequested, err := r.d.Runs.Heartbeat(ctx, runID)
			if err == nil && cancelRequested {
				cancel(errCancelled)
			}
		}
	}
}

// finish writes the final result and releases the job's concurrency slot
func (r *Runner) finish(ctx context.Context, run Run, final Final) {
	// A fresh context makes sure the result is recorded even when the run's context was canceled
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	ok, err := r.d.Runs.Finish(ctx, run.ID, final)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to record run result", slog.String("run", run.ID), slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	r.publishStatus(ctx, run, final.Status)
	r.d.Notifier.RunFinished(ctx, run, final)
}

func (r *Runner) publishStatus(ctx context.Context, run Run, status string) {
	events.PublishWorkspace(ctx, r.d.Bus, run.WorkspaceID, map[string]any{"kind": "run", "runId": run.ID, "jobId": run.JobID, "status": status})
	raw, _ := json.Marshal(events.Message{Kind: "status", Status: status})
	_ = r.d.Bus.Publish(ctx, events.RunTopic(run.ID), raw)
}

func startOfDay(t time.Time) int64 {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

// costObserver adds each agent model call's cost to the live run, so the broker can hold ump llm calls to the run's cost limit
type costObserver struct {
	agent.Observer
	live *LiveRun
}

func (o costObserver) OnLLMCall(turn int, resp *llm.Response, cost int64, latency time.Duration, err error) {
	o.live.AddAgentCost(cost)
	o.Observer.OnLLMCall(turn, resp, cost, latency, err)
}

// OnCompaction counts the summary call like any other agent call, since embedding hides the inner observer's optional methods
func (o costObserver) OnCompaction(c agent.Compaction) {
	o.live.AddAgentCost(c.Cost)
	if inner, ok := o.Observer.(agent.CompactionObserver); ok {
		inner.OnCompaction(c)
	}
}
