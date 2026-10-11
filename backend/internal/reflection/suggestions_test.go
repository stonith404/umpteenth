//go:build unit

package reflection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// suggestingJob is a job whose runs had 1024 MB of memory and a GitHub token
var suggestingJob = runner.JobConfig{
	Instruction: "Push the release notes", BaseImage: "sandbox:latest", ModelID: "m1",
	Env:    map[string]string{"GITHUB_TOKEN": "ghp-secret-value-123"},
	Limits: runner.Limits{TimeoutSeconds: 600, MaxTurns: 40, CPUs: 2, MemoryMB: 1024},
}

// suggestOp is a suggest operation in a reflection answer
func suggestOp(kind, name, text, content string) map[string]any {
	fields := map[string]any{"kind": kind, "text": text}
	if name != "" {
		fields["name"] = name
	}
	if content != "" {
		fields["content"] = content
	}
	return op(playbook.OpSuggest, fields)
}

// answerWith scripts one reflection answer with the given operations
func answerWith(t *testing.T, provider *fake.Provider, ops ...map[string]any) {
	t.Helper()
	list := make([]any, len(ops))
	for i, o := range ops {
		list[i] = o
	}
	provider.Enqueue(submit(t, map[string]any{"summary": "Needs the owner", "usedLearnings": []string{}, "ops": list}))
}

// storedSuggestion is a job_suggestions row as the tests check it
type storedSuggestion struct {
	Key, Kind, Text, Status string
	Limits, Before          sql.NullString
	RunID                   sql.NullString
	Seen                    int64
}

func loadSuggestions(t *testing.T, db *database.DB, jobID string) []storedSuggestion {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT key, kind, text, status, limits, limits_before, run_id, seen FROM job_suggestions WHERE job_id = $1 ORDER BY created_at, key`, jobID)
	require.NoError(t, err)
	defer rows.Close()
	var out []storedSuggestion
	for rows.Next() {
		var s storedSuggestion
		require.NoError(t, rows.Scan(&s.Key, &s.Kind, &s.Text, &s.Status, &s.Limits, &s.Before, &s.RunID, &s.Seen))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

// seedSuggestion adds a suggestion the way an earlier reflection or the owner left it, and returns its ID
func seedSuggestion(t *testing.T, db *database.DB, wid, jobID, key, kind, status string, resolvedAt *int64, limits *string) string {
	t.Helper()
	id := database.NewID()
	now := database.Now()
	testutil.Exec(t, db, `INSERT INTO job_suggestions (id, workspace_id, job_id, key, kind, text, limits, status, created_at, updated_at, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10)`, id, wid, jobID, key, kind, "Earlier text of "+key, limits, status, now, resolvedAt)
	return id
}

// markPending lets a run be reflected on again, like "Learn from this run" does
func markPending(t *testing.T, db *database.DB, runID string) {
	t.Helper()
	testutil.Exec(t, db, `UPDATE runs SET reflection = 'pending' WHERE id = $1`, runID)
}

func TestReflectionStoresASuggestionWithoutAPlaybookVersion(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)
	provider := fake.New()
	answerWith(t, provider, suggestOp("limits", "", "The build was killed at 1024 MB.", `{"memoryMb":4096}`))
	m := newModule(db, pb, provider, suggestingJob)

	// The suggestion waits for the owner, so the playbook gets no version
	res := reflectOn(t, m, wid, runID)
	assert.Nil(t, res.Version)
	require.Len(t, res.Suggestions, 1)
	require.Len(t, res.Ops, 1)
	assert.Equal(t, playbook.OpHeld, res.Ops[0].Status)
	assert.Equal(t, "limits", res.Ops[0].Target)

	// Recording the result stores the suggestion with the job's memory before it
	m.finish(ctx, wid, jobID, runID, nil, res)
	stored := loadSuggestions(t, db, jobID)
	require.Len(t, stored, 1)
	s := stored[0]
	assert.Equal(t, "limits", s.Key)
	assert.Equal(t, "limits", s.Kind)
	assert.Equal(t, SuggestionOpen, s.Status)
	assert.Equal(t, "The build was killed at 1024 MB.", s.Text)
	assert.JSONEq(t, `{"memoryMb":4096}`, s.Limits.String)
	assert.JSONEq(t, `{"memoryMb":1024}`, s.Before.String)
	assert.Equal(t, runID, s.RunID.String)
	assert.EqualValues(t, 1, s.Seen)

	// The run's Learned tab lists the suggestion as held
	var opsJSON string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT reflection_ops FROM runs WHERE id = $1`, runID).Scan(&opsJSON))
	var ops []playbook.AppliedOp
	require.NoError(t, json.Unmarshal([]byte(opsJSON), &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, playbook.OpHeld, ops[0].Status)
	assert.Equal(t, "limits", ops[0].Target)
	var versions int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playbook_versions WHERE job_id = $1`, jobID).Scan(&versions))
	assert.Zero(t, versions)
}

func TestALaterRunUpdatesTheOpenSuggestion(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	m := newModule(db, pb, provider, suggestingJob)

	// The first run raises the subject
	first := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)
	answerWith(t, provider, suggestOp("credentials", "github_token_workflow_scope", "GITHUB_TOKEN lacks the workflow scope.", ""))
	m.finish(ctx, wid, jobID, first, nil, reflectOn(t, m, wid, first))

	// The second run sees it waiting and raises it again with more detail
	second := seedRun(t, db, wid, jobID, 2, runner.ModeAssisted, 0)
	answerWith(t, provider, suggestOp("credentials", "github_token_workflow_scope", "GITHUB_TOKEN lacks the workflow scope, so pushing .github/workflows failed twice.", ""))
	m.finish(ctx, wid, jobID, second, nil, reflectOn(t, m, wid, second))
	requests := provider.Requests()
	assert.Contains(t, requests[len(requests)-1].Messages[0].Text(), "- waiting (credentials, raised by 1 run(s)) github_token_workflow_scope: GITHUB_TOKEN lacks the workflow scope.")
	stored := loadSuggestions(t, db, jobID)
	require.Len(t, stored, 1)
	assert.EqualValues(t, 2, stored[0].Seen)
	assert.Equal(t, second, stored[0].RunID.String)
	assert.Contains(t, stored[0].Text, "failed twice")

	// Learning from the same run again rewords it without counting the run twice
	markPending(t, db, second)
	answerWith(t, provider, suggestOp("credentials", "github_token_workflow_scope", "GITHUB_TOKEN needs the workflow scope.", ""))
	m.finish(ctx, wid, jobID, second, nil, reflectOn(t, m, wid, second))
	stored = loadSuggestions(t, db, jobID)
	require.Len(t, stored, 1)
	assert.EqualValues(t, 2, stored[0].Seen)
	assert.Equal(t, "GITHUB_TOKEN needs the workflow scope.", stored[0].Text)
}

func TestDismissedSuggestionsAreNotRaisedAgain(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	m := newModule(db, pb, provider, suggestingJob)

	// The owner dismissed one subject yesterday and another one long ago
	seedSuggestion(t, db, wid, jobID, "email_on_weekends", "instruction", SuggestionDismissed, new(time.Now().Add(-24*time.Hour).UnixMilli()), nil)
	seedSuggestion(t, db, wid, jobID, "github_token_workflow_scope", "credentials", SuggestionDismissed, new(time.Now().Add(-31*24*time.Hour).UnixMilli()), nil)

	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)
	answerWith(t, provider,
		suggestOp("instruction", "email_on_weekends", "Should the email go out on weekends?", ""),
		suggestOp("credentials", "github_token_workflow_scope", "GITHUB_TOKEN lacks the workflow scope.", ""),
	)
	res := reflectOn(t, m, wid, runID)

	// Reflection was told about the recent dismissal only
	first := provider.Requests()[0].Messages[0].Text()
	assert.Contains(t, first, "## Suggestions to the owner")
	assert.Contains(t, first, "- dismissed, don't raise again (instruction) email_on_weekends")
	assert.NotContains(t, first, "github_token_workflow_scope")

	// The recently dismissed subject is rejected, and the one dismissed long ago comes back
	require.Len(t, res.Ops, 2)
	assert.Equal(t, playbook.OpRejected, res.Ops[0].Status)
	assert.Contains(t, res.Ops[0].Reason, "dismissed")
	assert.Equal(t, playbook.OpHeld, res.Ops[1].Status)
	m.finish(ctx, wid, jobID, runID, nil, res)
	var open []string
	for _, s := range loadSuggestions(t, db, jobID) {
		if s.Status == SuggestionOpen {
			open = append(open, s.Key)
		}
	}
	assert.Equal(t, []string{"github_token_workflow_scope"}, open)
}

func TestOpenSuggestionsAreCapped(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	m := newModule(db, pb, provider, suggestingJob)
	for i := range maxOpenSuggestions {
		seedSuggestion(t, db, wid, jobID, fmt.Sprintf("subject_%d", i), "instruction", SuggestionOpen, nil, nil)
	}

	// A new subject would go over the cap, while a waiting one can still be updated
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)
	answerWith(t, provider,
		suggestOp("instruction", "subject_new", "One more question.", ""),
		suggestOp("instruction", "subject_3", "A sharper question.", ""),
	)
	res := reflectOn(t, m, wid, runID)
	require.Len(t, res.Ops, 2)
	assert.Equal(t, playbook.OpRejected, res.Ops[0].Status)
	assert.Contains(t, res.Ops[0].Reason, "already has 10 open suggestions")
	assert.Equal(t, playbook.OpHeld, res.Ops[1].Status)

	m.finish(ctx, wid, jobID, runID, nil, res)
	stored := loadSuggestions(t, db, jobID)
	require.Len(t, stored, maxOpenSuggestions)
	for _, s := range stored {
		if s.Key == "subject_3" {
			assert.Equal(t, "A sharper question.", s.Text)
			assert.EqualValues(t, 2, s.Seen)
		}
	}
}

func TestALostClaimStoresNoSuggestions(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	m := newModule(db, pb, provider, suggestingJob)
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)
	answerWith(t, provider, suggestOp("credentials", "github_token_workflow_scope", "GITHUB_TOKEN lacks the workflow scope.", ""))
	res := reflectOn(t, m, wid, runID)
	require.Len(t, res.Suggestions, 1)

	// Another claim took the run over, so this result is too late and leaves no suggestion behind
	m.finish(t.Context(), wid, jobID, runID, new(int64(1)), res)
	assert.Empty(t, loadSuggestions(t, db, jobID))
}

// fakeEditor records the limits suggestions apply with the values they saw, and fails when told to
type fakeEditor struct {
	applied []playbook.LimitChange
	before  []playbook.LimitChange
	err     error
	// during runs while the limits are being set, as a request racing the apply would
	during func()
}

func (f *fakeEditor) JobExists(context.Context, string, string) error { return nil }

func (f *fakeEditor) ApplyLimits(_ context.Context, _, _ string, change, before playbook.LimitChange) error {
	if f.err != nil {
		return f.err
	}
	if f.during != nil {
		f.during()
	}
	f.applied = append(f.applied, change)
	f.before = append(f.before, before)
	return nil
}

func TestApplyAndDismissSuggestion(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	otherJobID := testutil.SeedJob(t, db, wid, "skip")
	otherWid := testutil.SeedWorkspace(t, db)
	editor := &fakeEditor{}
	m := newModule(db, nil, nil, runner.JobConfig{})
	m.deps.JobEditor = editor
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	status := func(id string) string {
		var s string
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT status FROM job_suggestions WHERE id = $1`, id).Scan(&s))
		return s
	}
	apply := func(ctx context.Context, jobID, id string) error {
		_, err := m.applySuggestion(ctx, &suggestionInput{ID: jobID, SuggestionID: id})
		return err
	}

	// Applying a limits suggestion sets the limits it names and closes it, and a second click finds it closed
	limits := seedSuggestion(t, db, wid, jobID, "limits", "limits", SuggestionOpen, nil, new(`{"memoryMb":4096}`))
	require.NoError(t, apply(ctx, jobID, limits))
	assert.Equal(t, []playbook.LimitChange{{MemoryMB: new(4096)}}, editor.applied)
	assert.Equal(t, SuggestionDone, status(limits))
	assert.True(t, apperror.IsCode(apply(ctx, jobID, limits), apperror.CodeConflict))

	// The limits the suggestion saw when it was raised go along, so a limit changed by hand since is kept
	seen := seedSuggestion(t, db, wid, jobID, "limits", "limits", SuggestionOpen, nil, new(`{"memoryMb":2048}`))
	testutil.Exec(t, db, `UPDATE job_suggestions SET limits_before = $1 WHERE id = $2`, `{"memoryMb":1024}`, seen)
	require.NoError(t, apply(ctx, jobID, seen))
	assert.Equal(t, playbook.LimitChange{MemoryMB: new(1024)}, editor.before[1])

	// A dismissal racing the apply comes after the job got the limits, so the suggestion ends up done rather than dismissed
	raced := seedSuggestion(t, db, wid, jobID, "limits", "limits", SuggestionOpen, nil, new(`{"maxTurns":80}`))
	editor.during = func() {
		_, err := m.dismissSuggestion(ctx, &suggestionInput{ID: jobID, SuggestionID: raced})
		assert.NoError(t, err)
	}
	require.NoError(t, apply(ctx, jobID, raced))
	editor.during = nil
	assert.Equal(t, SuggestionDone, status(raced))

	// When the job can't take the limits, the suggestion stays open to try again
	editor.err = errors.New("the job was changed at the same time")
	retry := seedSuggestion(t, db, wid, jobID, "limits", "limits", SuggestionOpen, nil, new(`{"cpus":4}`))
	require.ErrorIs(t, apply(ctx, jobID, retry), editor.err)
	assert.Equal(t, SuggestionOpen, status(retry))
	editor.err = nil

	// Other kinds are changed by hand, so applying them only marks them as done
	credentials := seedSuggestion(t, db, wid, jobID, "github_token_workflow_scope", "credentials", SuggestionOpen, nil, nil)
	require.NoError(t, apply(ctx, jobID, credentials))
	assert.Equal(t, SuggestionDone, status(credentials))
	assert.Len(t, editor.applied, 3)

	// Dismissing closes a suggestion without changing the job
	instruction := seedSuggestion(t, db, wid, jobID, "email_on_weekends", "instruction", SuggestionOpen, nil, nil)
	_, err := m.dismissSuggestion(ctx, &suggestionInput{ID: jobID, SuggestionID: instruction})
	require.NoError(t, err)
	assert.Equal(t, SuggestionDismissed, status(instruction))
	_, err = m.dismissSuggestion(ctx, &suggestionInput{ID: jobID, SuggestionID: instruction})
	assert.True(t, apperror.IsCode(err, apperror.CodeConflict))

	// A suggestion is only reached through its own job and workspace
	other := seedSuggestion(t, db, wid, otherJobID, "email_on_weekends", "instruction", SuggestionOpen, nil, nil)
	assert.True(t, apperror.IsCode(apply(ctx, jobID, other), apperror.CodeNotFound))
	foreign := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: otherWid})
	assert.True(t, apperror.IsCode(apply(foreign, otherJobID, other), apperror.CodeNotFound))
	assert.Equal(t, SuggestionOpen, status(other))

	// The list holds only the job's suggestions that still wait for a decision
	out, err := m.listSuggestions(ctx, &jobIDInput{ID: jobID})
	require.NoError(t, err)
	require.Len(t, out.Body, 1)
	assert.Equal(t, retry, out.Body[0].ID)
	assert.Equal(t, &playbook.LimitChange{CPUs: new(4.0)}, out.Body[0].Limits)
}

func TestReflectionInputListsLimitsAndSecretNames(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	provider := fake.New()
	answerWith(t, provider)
	m := newModule(db, pb, provider, suggestingJob)
	reflectOn(t, m, wid, seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0))

	// Reflection knows what only the owner can change, and the secrets by name but never by value
	first := provider.Requests()[0].Messages[0].Text()
	assert.Contains(t, first, "Limits per run, which only the owner can change: 2 CPU, 1024 MB memory, 600 seconds, 40 turns")
	assert.Contains(t, first, "Secrets as environment variables: GITHUB_TOKEN")
	assert.NotContains(t, first, "ghp-secret-value-123")
	assert.NotContains(t, first, "## Suggestions to the owner")
}

func TestAChangedInstructionIsReflectedOnOnce(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid, jobID, runID, pb := graduatedJob(t, db)
	testutil.Exec(t, db, `UPDATE jobs SET instruction_changed_at = 42 WHERE id = $1`, jobID)
	stamp := func() sql.NullInt64 {
		var at sql.NullInt64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT instruction_changed_at FROM jobs WHERE id = $1`, jobID).Scan(&at))
		return at
	}

	// A main script written for the old instruction is checked against the new one
	provider := fake.New()
	answerWith(t, provider)
	job := mp3Job
	job.InstructionChangedAt = new(int64(42))
	m := newModule(db, pb, provider, job)
	res := reflectOn(t, m, wid, runID)
	assert.Contains(t, provider.Requests()[0].Messages[0].Text(), "## The instruction changed")

	// Recording the result clears the stamp it saw, so later successful scripted runs need no reflection for it
	m.finish(ctx, wid, jobID, runID, nil, res)
	assert.False(t, stamp().Valid)

	// An instruction that changed again meanwhile keeps its stamp for the next reflection
	testutil.Exec(t, db, `UPDATE jobs SET instruction_changed_at = 43 WHERE id = $1`, jobID)
	markPending(t, db, runID)
	answerWith(t, provider)
	res = reflectOn(t, m, wid, runID)
	m.finish(ctx, wid, jobID, runID, nil, res)
	assert.Equal(t, int64(43), stamp().Int64)
}
