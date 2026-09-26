//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/jobs"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

type models struct{ p llm.Provider }

func (m models) Resolve(context.Context, string, string) (llm.Provider, runner.Model, error) {
	return m.p, runner.Model{ID: "m1", Name: "fake-model", Price: llm.Price{In: 3_000_000, Out: 15_000_000}}, nil
}

type states struct{ jobs *jobs.Module }

func (s states) ForJob(wid, jobID string) agent.StateStore { return s.jobs.ForJob(wid, jobID) }

type harness struct {
	db       *database.DB
	runner   *runner.Runner
	adapter  *sandboxfake.Adapter
	provider *fake.Provider
	storage  storage.FileStorage
	wid      string
	jobID    string
	// finals are the results the runner reported when runs finished
	finals []runner.Final
	live   *runner.Registry
	runs   *runs.Module
}

func (h *harness) RunFinished(_ context.Context, _ runner.Run, final runner.Final) {
	h.finals = append(h.finals, final)
}

// baseImages resolves every job to its base image, like a job without a Dockerfile
type baseImages struct{}

func (baseImages) ResolveImage(_ context.Context, job runner.JobConfig, _ func()) (string, string, error) {
	return job.BaseImage, "", nil
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithImages(t, baseImages{})
}

// newHarnessWithImages wires an image resolver, e.g. one that waits for a job image to build
func newHarnessWithImages(t *testing.T, images runner.ImageResolver) *harness {
	h := &harness{db: testutil.NewDatabaseForTest(t), adapter: sandboxfake.New(), provider: fake.New(), live: runner.NewRegistry()}
	var err error
	h.storage, err = storage.NewFilesystemStorage(t.TempDir())
	require.NoError(t, err)

	var runsModule *runs.Module
	var jobsModule *jobs.Module
	bus := events.NewLocalBus()
	settingsModule := settings.New(settings.Dependencies{DB: h.db, Defaults: settings.Defaults{Image: "img", RetentionDays: 90}})
	testutil.NewActorHostForTest(t, func(t *testing.T, host *local.Host) {
		runsModule, err = runs.New(runs.Dependencies{DB: h.db, Actors: host, Bus: bus, Storage: h.storage, Adapter: h.adapter, MaxConcurrentRuns: 1, MaintenanceDisabled: true})
		require.NoError(t, err)
		jobsModule, err = jobs.New(jobs.Dependencies{DB: h.db, Actors: host, Runs: runsModule, Playbooks: playbook.New(playbook.Dependencies{DB: h.db}), Settings: settingsModule})
		require.NoError(t, err)
	})

	h.wid = testutil.SeedWorkspace(t, h.db)
	h.jobID = testutil.SeedJob(t, h.db, h.wid, jobs.ConcurrencySkip)
	// The workspace default model applies, since the job has no model of its own
	testutil.Exec(t, h.db, "INSERT INTO settings (workspace_id, key, value) VALUES ($1, 'agentModelId', '\"m1\"')", h.wid)

	h.runs = runsModule
	h.runner = runner.New(runner.Deps{
		DB: h.db, Runs: runsModule.Store(), Jobs: jobsModule, Models: models{h.provider}, State: states{jobsModule},
		Adapter: h.adapter, Images: images, Live: h.live, Bus: bus, Storage: h.storage, HostID: "test", BrokerPort: 8081,
		Notifier: h, Cancel: runsModule,
		UtilityModel: func(context.Context, string) (string, error) { return "", nil },
	})
	return h
}

func (h *harness) seedRun(t *testing.T, status string) string {
	id := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, $4, 'explore', 'manual', 0, $5)`,
		id, h.wid, h.jobID, status, database.Now())
	return id
}

func (h *harness) run(t *testing.T, id string) (status, errMsg, summary, outputs string, cost int64) {
	var e, s, o *string
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT status, error, summary, outputs, cost FROM runs WHERE id = $1", id).Scan(&status, &e, &s, &o, &cost))
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return status, deref(e), deref(s), deref(o), cost
}

func TestExecuteRunsTheAgentInASandbox(t *testing.T) {
	h := newHarness(t)
	h.adapter.On("ls /", sandboxfake.Reply{Stdout: "bin\netc\n"})
	h.provider.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "ls /"}}}, Usage: llm.Usage{Input: 1000, Output: 100}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{
			{Name: "state_set", Args: map[string]any{"key": "seen", "value": "etc"}},
			{Name: "finish", Args: map[string]any{"status": "success", "summary": "Listed /", "outputs": map[string]any{"count": 2}}},
		}, Usage: llm.Usage{Input: 2000, Output: 200}},
	)
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	status, errMsg, summary, outputs, cost := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	require.Equal(t, "Listed /", summary)
	require.JSONEq(t, `{"count":2}`, outputs)
	// 3000 input tokens at $3/M plus 300 output tokens at $15/M
	require.EqualValues(t, 9000+4500, cost)

	// The sandbox is gone and the command ran through bash as the agent user
	list, _ := h.adapter.List(context.Background())
	require.Empty(t, list)
	var sawBash bool
	for _, c := range h.adapter.Calls() {
		if len(c.Cmd) > 0 && c.Cmd[0] == "bash" {
			sawBash = true
			require.Equal(t, sandbox.UserAgent, c.User)
		}
	}
	require.True(t, sawBash)

	// Job state was persisted, and the timeline has the expected shape
	var seen string
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT value FROM job_state WHERE job_id = $1 AND key = 'seen'", h.jobID).Scan(&seen))
	require.Equal(t, "etc", seen)

	rows, err := h.db.QueryContext(context.Background(), "SELECT type FROM run_events WHERE run_id = $1 ORDER BY seq", id)
	require.NoError(t, err)
	var types []string
	for rows.Next() {
		var ty string
		require.NoError(t, rows.Scan(&ty))
		types = append(types, ty)
	}
	require.NoError(t, rows.Close())
	require.Contains(t, types, events.TypeSandboxCreate)
	require.Contains(t, types, events.TypeLLMCall)
	require.Contains(t, types, events.TypeToolResult)
	require.Contains(t, types, events.TypeFinish)
	require.Equal(t, events.TypeRunStatus, types[len(types)-1])
}

func TestExecuteNeverReExecutesAnInterruptedRun(t *testing.T) {
	h := newHarness(t)
	id := h.seedRun(t, runner.StatusRunning)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusFailed, status)
	require.Contains(t, errMsg, "interrupted")
	require.Empty(t, h.adapter.Calls(), "no sandbox work happens for a redelivered run")
	require.Empty(t, h.provider.Requests())
}

func TestExecuteIgnoresFinishedRuns(t *testing.T) {
	h := newHarness(t)
	id := h.seedRun(t, runner.StatusCancelled)
	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, _, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusCancelled, status)
}

func TestExecuteFailsClearlyWithoutAModel(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, "DELETE FROM settings WHERE workspace_id = $1", h.wid)
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusFailed, status)
	require.Contains(t, errMsg, "No model is configured")
}

func TestAgentFailureIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "failure", "summary": "API down"}}}})
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, summary, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusFailed, status)
	require.Equal(t, "API down", summary)
	require.NotEmpty(t, errMsg)

	var raw string
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT payload FROM run_events WHERE run_id = $1 AND type = 'finish'", id).Scan(&raw))
	var f agent.Finish
	require.NoError(t, json.Unmarshal([]byte(raw), &f))
	require.Equal(t, "failure", f.Status)
}

func TestToolkitScriptsBecomeToolsAndCandidatesAreKept(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// The job's playbook has one proven script
	script := "#!/usr/bin/env python3\n# ump:name top_stories\n# ump:description Print the top stories\n# ump:args {\"count\":\"integer\"}\nprint('[]')\n"
	pb := playbook.New(playbook.Dependencies{DB: h.db})
	next, results := playbook.Apply(playbook.Content{}, []playbook.Op{{Op: playbook.OpUpsertScript, Content: &script}}, playbook.ApplyContext{BaseImage: "img"})
	require.Equal(t, playbook.OpApplied, results[0].Status, results[0].Reason)
	_, err := pb.Save(ctx, h.wid, h.jobID, next, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)

	h.adapter.OnFunc(func(cmd []string) bool { return len(cmd) > 3 && cmd[3] == "ump-toolkit" }, sandboxfake.Reply{Stdout: `[{"title":"a"}]`}.Handler())
	h.adapter.OnFunc(func(cmd []string) bool { return len(cmd) > 3 && cmd[3] == "ump-write" }, func(ctx context.Context, sb *sandboxfake.Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		content, err := io.ReadAll(req.Stdin)
		if err != nil {
			return sandbox.ExecResult{ExitCode: 1}, err
		}
		return sandbox.ExecResult{}, sb.PutFiles(ctx, []sandbox.File{{Path: req.Cmd[4], Mode: 0o644, Content: content}})
	})
	h.provider.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{
			{Name: "toolkit__top_stories", Args: map[string]any{"count": 2}},
			{Name: "write_file", Args: map[string]any{"path": "/ump/candidates/format.py", "content": "# ump:name format\nprint()"}},
		}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}},
	)
	id := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'queued', 'assisted', 'manual', 1, $4)`,
		id, h.wid, h.jobID, database.Now())

	require.NoError(t, h.runner.Execute(ctx, id))
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)

	// The script was offered as a tool, and its call was counted for the playbook's stats
	var offered []string
	for _, tool := range h.provider.Requests()[0].Tools {
		offered = append(offered, tool.Name)
	}
	require.Contains(t, offered, "toolkit__top_stories")
	require.Len(t, h.finals, 1)
	require.Equal(t, map[string]runner.ToolkitUsage{"top_stories": {Calls: 1}}, h.finals[0].Toolkit)

	// The candidate script outlives the sandbox, so reflection can promote it
	r, _, err := h.storage.Open(ctx, "runs/"+id+"/candidates/format.py")
	require.NoError(t, err)
	_ = r.Close()
}

// onBash answers the agent's bash commands with a handler
func (h *harness) onBash(handler sandboxfake.Handler) {
	h.adapter.OnFunc(func(cmd []string) bool { return len(cmd) > 0 && cmd[0] == "bash" }, handler)
}

func TestCancelEndsTheRunAsCancelled(t *testing.T) {
	h := newHarness(t)
	id := h.seedRun(t, runner.StatusQueued)

	// The command cancels its own run and then runs until the cancel stops it
	h.onBash(func(ctx context.Context, _ *sandboxfake.Sandbox, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
		require.NoError(t, h.runs.Cancel(context.Background(), h.wid, id))
		<-ctx.Done()
		return sandbox.ExecResult{}, ctx.Err()
	})
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "sleep 600"}}}})

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	assert.Equal(t, runner.StatusCancelled, status)
	assert.Equal(t, "Cancelled", errMsg)
}

func TestFailedSetupScriptIsLearnedFrom(t *testing.T) {
	h := newHarness(t)
	testutil.Exec(t, h.db, "UPDATE jobs SET self_improve = TRUE WHERE id = $1", h.jobID)
	setup := "apt-get install -y missing-package"
	_, err := playbook.New(playbook.Dependencies{DB: h.db}).Save(context.Background(), h.wid, h.jobID, playbook.Content{Setup: &setup}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)
	h.adapter.On(setup, sandboxfake.Reply{Stderr: "E: Unable to locate package", ExitCode: 100})
	id := database.NewID()
	testutil.Exec(t, h.db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'queued', 'explore', 'manual', 1, $4)`,
		id, h.wid, h.jobID, database.Now())

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// The agent never ran, but the playbook's own setup script broke the run, which reflection can repair
	require.Len(t, h.finals, 1)
	assert.Equal(t, runner.StatusFailed, h.finals[0].Status)
	assert.True(t, h.finals[0].SetupFailed)
	assert.Equal(t, runner.ReflectionPending, h.finals[0].Reflection)
	assert.Empty(t, h.provider.Requests())
}

func TestArtifactsKeepTheirPaths(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.onBash(func(ctx context.Context, sb *sandboxfake.Sandbox, _ sandbox.ExecRequest) (sandbox.ExecResult, error) {
		return sandbox.ExecResult{}, sb.PutFiles(ctx, []sandbox.File{{Path: "/ump/outputs/outputs/report.txt", Mode: 0o644, Content: []byte("done")}})
	})
	h.provider.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "make report"}}}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}},
	)
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(ctx, id))

	// A directory inside the outputs that shares their name stays part of the artifact's path
	r, _, err := h.storage.Open(ctx, "runs/"+id+"/artifacts/outputs/report.txt")
	require.NoError(t, err)
	_ = r.Close()
}

func TestAgentRunCountsUmpLLMCallsLeftRunning(t *testing.T) {
	h := newHarness(t)
	id := h.seedRun(t, runner.StatusQueued)

	// The agent starts a ump llm call in the background and finishes before it answers
	h.onBash(func(context.Context, *sandboxfake.Sandbox, sandbox.ExecRequest) (sandbox.ExecResult, error) {
		live, ok := h.live.Get(id)
		require.True(t, ok)
		_, ok = live.ReserveSpend(5000)
		require.True(t, ok)
		return sandbox.ExecResult{}, nil
	})
	h.provider.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "ump llm 'Summarize the log' > summary.txt &"}}}, Usage: llm.Usage{Input: 1000, Output: 100}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "Started the summary"}}}, Usage: llm.Usage{Input: 2000, Output: 200}},
	)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	// The agent's own calls cost 13500, and the call it left running counts with its reservation
	status, errMsg, _, _, cost := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)
	assert.EqualValues(t, 9000+4500+5000, cost)
}
