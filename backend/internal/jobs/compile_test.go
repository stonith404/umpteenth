//go:build unit

package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// fakeModels resolves every model to one fake provider
type fakeModels struct{ provider llm.Provider }

func (f fakeModels) ResolveModel(context.Context, string, string) (llm.Provider, string, error) {
	return f.provider, "fake-model", nil
}

func (f fakeModels) ModelExists(context.Context, string, string) (bool, error) { return true, nil }

// compileFixture is a module whose compile step answers from a fake provider, and a job to edit
type compileFixture struct {
	m        *Module
	provider *fake.Provider
	settings *settings.Module
	wid      string
	jobID    string
}

func newCompileFixture(t *testing.T) compileFixture {
	db := testutil.NewDatabaseForTest(t)
	provider := fake.New()
	sm := settings.New(settings.Dependencies{DB: db, Defaults: settings.Defaults{Image: "img", RetentionDays: 90}})
	var m *Module
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		var err error
		m, err = New(Dependencies{
			DB:        db,
			Actors:    h,
			Runs:      &fakeQueue{db: db},
			Playbooks: playbook.New(playbook.Dependencies{DB: db}),
			Settings:  sm,
			Models:    fakeModels{provider: provider},
		})
		require.NoError(t, err)
	})

	// The job was compiled from its first instruction, then renamed and given a schedule in its settings
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	job, err := m.createJob(ctx, wid, nil, jobFields{
		Name:        "Stale PRs",
		Instruction: "Post stale PRs of acme/api to #eng",
		Spec: &Spec{
			Title:           "Stale PRs",
			Goal:            "Post stale PRs",
			Schedule:        &Schedule{Cron: "0 8 * * *", Timezone: "UTC", Human: "Daily at 08:00"},
			SuccessCriteria: []string{"One message is posted to #eng"},
			Outputs:         []IOField{{Name: "count", Type: "integer"}},
			Network:         "internet",
		},
	})
	require.NoError(t, err)
	return compileFixture{m: m, provider: provider, settings: sm, wid: wid, jobID: job.ID}
}

// useModel gives the workspace a utility model, without which nothing is compiled
func (f compileFixture) useModel(t *testing.T) {
	require.NoError(t, f.settings.Set(context.Background(), f.wid, "utilityModelId", "model-1"))
}

// answer scripts the compile step's next spec
func (f compileFixture) answer(t *testing.T, spec map[string]any) {
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	f.provider.Enqueue(fake.ScriptedResponse{Text: string(raw)})
}

// patch applies a patch the way the update endpoint does, and returns the stored spec
func (f compileFixture) patch(t *testing.T, p jobPatch) Spec {
	ctx := context.Background()
	p, err := f.m.withRebuiltSpec(ctx, f.wid, f.jobID, p)
	require.NoError(t, err)
	applied, err := f.m.applyPatch(ctx, f.wid, f.jobID, p)
	require.NoError(t, err)
	require.True(t, applied)
	job, err := f.m.getJob(ctx, f.wid, f.jobID)
	require.NoError(t, err)
	d, err := toDto(job)
	require.NoError(t, err)
	return d.Spec
}

// storedSpec is what the settings pages send back along with every change
func (f compileFixture) storedSpec(t *testing.T) *Spec {
	job, err := f.m.getJob(context.Background(), f.wid, f.jobID)
	require.NoError(t, err)
	d, err := toDto(job)
	require.NoError(t, err)
	return &d.Spec
}

func TestEditedInstructionRebuildsTheSpec(t *testing.T) {
	f := newCompileFixture(t)
	f.useModel(t)
	f.answer(t, map[string]any{
		"title": "Something else", "goal": "Post stale PRs to #ops", "schedule": nil,
		"successCriteria": []string{"One message is posted to #ops"},
		"inputs":          []any{}, "outputs": []map[string]any{{"name": "count", "type": "integer", "description": "Stale PRs"}},
		"mcp":     []map[string]any{{"server": "Slack", "why": "post the digest"}},
		"network": "none", "dockerfile": "FROM img", "sideEffects": []string{"Posts to Slack"},
		"questions": []map[string]any{{"question": "Which channel?", "options": []string{}}},
	})

	// The general settings card sends the stored spec with the new name and instruction
	sent := f.storedSpec(t)
	sent.Title = "Stale PRs to ops"
	spec := f.patch(t, jobPatch{Name: new("Stale PRs to ops"), Instruction: new("Post stale PRs of acme/api to #ops"), Spec: sent})

	// The description comes from the new instruction, and what mirrors the settings stays as the patch left it
	require.Equal(t, "Post stale PRs to #ops", spec.Goal)
	require.Equal(t, []string{"One message is posted to #ops"}, spec.SuccessCriteria)
	require.Equal(t, []string{"Posts to Slack"}, spec.SideEffects)
	require.Equal(t, "Slack", spec.MCP[0].Server)
	require.Equal(t, "Stale PRs to ops", spec.Title)
	require.Equal(t, "0 8 * * *", spec.Schedule.Cron)
	require.Equal(t, "internet", spec.Network)
	require.Nil(t, spec.Dockerfile)

	// The compile step saw the spec it replaces, so it can keep output names, and was told not to ask
	requests := f.provider.Requests()
	require.Len(t, requests, 1)
	prompt := requests[0].Messages[0].Text()
	require.Contains(t, prompt, "Post stale PRs of acme/api to #ops")
	require.Contains(t, prompt, "One message is posted to #eng")
	require.Contains(t, prompt, "Ask no questions")
}

func TestSpecIsKeptWhenTheInstructionDoesNotChange(t *testing.T) {
	f := newCompileFixture(t)
	f.useModel(t)

	// Saving the unchanged instruction again, even with other spacing, costs no model call
	spec := f.patch(t, jobPatch{Name: new("Renamed"), Instruction: new("  Post stale PRs of acme/api to #eng\n"), Spec: f.storedSpec(t)})
	require.Equal(t, "Post stale PRs", spec.Goal)
	require.Empty(t, f.provider.Requests())
}

func TestPatchWithItsOwnDescriptionKeepsIt(t *testing.T) {
	f := newCompileFixture(t)
	f.useModel(t)

	// An API client that sends a new instruction together with a spec it wrote gets that spec
	own := f.storedSpec(t)
	own.SuccessCriteria = []string{"Written by hand"}
	spec := f.patch(t, jobPatch{Instruction: new("Something new"), Spec: own})
	require.Equal(t, []string{"Written by hand"}, spec.SuccessCriteria)
	require.Empty(t, f.provider.Requests())
}

func TestSpecIsKeptWhenTheRebuildIsTurnedOff(t *testing.T) {
	f := newCompileFixture(t)
	f.useModel(t)

	// A spec edited by hand survives a new instruction when the settings ask to keep it
	spec := f.patch(t, jobPatch{Instruction: new("Something new"), Spec: f.storedSpec(t), RebuildSpec: new(false)})
	require.Equal(t, "Post stale PRs", spec.Goal)
	require.Equal(t, []string{"One message is posted to #eng"}, spec.SuccessCriteria)
	require.Empty(t, f.provider.Requests())
}

func TestSpecIsKeptWithoutAModelToCompileWith(t *testing.T) {
	f := newCompileFixture(t)

	// Without a model the job was described by hand, so the instruction is saved and the spec stays
	spec := f.patch(t, jobPatch{Instruction: new("Something new")})
	require.Equal(t, "Post stale PRs", spec.Goal)
	require.Empty(t, f.provider.Requests())
}

func TestFailedRebuildSavesNothing(t *testing.T) {
	f := newCompileFixture(t)
	f.useModel(t)
	f.provider.Enqueue(fake.ScriptedResponse{Error: "provider down"})

	// A spec that could not be rebuilt would describe the old instruction, so the edit is refused as a whole
	_, err := f.m.withRebuiltSpec(context.Background(), f.wid, f.jobID, jobPatch{Instruction: new("Something new")})
	require.Error(t, err)
	job, err := f.m.getJob(context.Background(), f.wid, f.jobID)
	require.NoError(t, err)
	require.Equal(t, "Post stale PRs of acme/api to #eng", job.Instruction)
}

func TestCleanQuestions(t *testing.T) {
	asked := []Question{
		{Question: " Which channel? ", Options: []string{"#eng", " #eng ", "", "#ops", "#dev", "#qa", "#all"}},
		{Question: "  ", Options: []string{"x"}},
		{Question: "Which repo?"},
		{Question: "Which branch?"},
		{Question: "Which label?"},
	}

	// Blank questions and answers go, duplicates collapse, and the lists stop at their caps
	got := cleanQuestions(asked, true)
	require.Len(t, got, maxQuestions)
	require.Equal(t, "Which channel?", got[0].Question)
	require.Equal(t, []string{"#eng", "#ops", "#dev", "#qa"}, got[0].Options)
	require.Equal(t, "Which repo?", got[1].Question)
	require.Equal(t, []string{}, got[1].Options)

	// Once the user answered, nothing is asked again
	require.Empty(t, cleanQuestions(asked, false))
}
