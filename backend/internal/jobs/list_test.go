//go:build unit

package jobs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestListShowsRecentRunsAndTheNextMode(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	pb := m.deps.Playbooks.(*playbook.Module)

	seedJob := func(name string) string {
		t.Helper()
		id := testutil.SeedJob(t, db, wid, ConcurrencySkip)
		testutil.Exec(t, db, "UPDATE jobs SET name = $1 WHERE id = $2", name, id)
		return id
	}
	seedRun := func(jobID string, number int64, status, mode string, fellBack bool, version int64) {
		t.Helper()
		testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, fell_back, trigger, playbook_version, queued_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'manual', $8, $9)`, database.NewID(), wid, jobID, number, status, mode, fellBack, version, 1_000_000+number)
	}
	save := func(jobID string, c playbook.Content) int64 {
		t.Helper()
		v, err := pb.Save(ctx, wid, jobID, c, playbook.VersionMeta{Author: playbook.AuthorReflection})
		require.NoError(t, err)
		return v
	}
	learning := []playbook.Learning{{ID: "L1", Kind: "fact", Text: "x", Status: "active"}}
	main := "#!/bin/sh\necho ok\n"

	// A busy job with an empty playbook shows its ten latest runs and explores next
	busy := seedJob("a busy")
	testutil.Exec(t, db, "UPDATE jobs SET spec = $1 WHERE id = $2", `{"inputs":[{"name":"url","type":"string","description":"The page to check"}]}`, busy)
	for n := int64(1); n <= 12; n++ {
		status := "succeeded"
		if n%4 == 0 {
			status = "failed"
		}
		seedRun(busy, n, status, ModeExplore, false, 0)
	}

	// A job that learned something but never ran assists next and has no history
	fresh := seedJob("b fresh")
	save(fresh, playbook.Content{Learnings: learning})

	// A graduated job whose last two scripted runs fell back is demoted to Assisted, while the one that didn't stays Scripted
	demoted := seedJob("c demoted")
	v := save(demoted, playbook.Content{Learnings: learning, Main: &main})
	seedRun(demoted, 1, "succeeded", ModeScripted, true, v)
	seedRun(demoted, 2, "succeeded", ModeScripted, true, v)
	scripted := seedJob("d scripted")
	v = save(scripted, playbook.Content{Learnings: learning, Main: &main})
	seedRun(scripted, 1, "succeeded", ModeScripted, true, v)
	seedRun(scripted, 2, "succeeded", ModeScripted, false, v)

	// A job with graduation turned off stays on the agent although its main script never fell back
	agentOnly := seedJob("e agent only")
	v = save(agentOnly, playbook.Content{Learnings: learning, Main: &main})
	seedRun(agentOnly, 1, "succeeded", ModeScripted, false, v)
	testutil.Exec(t, db, "UPDATE jobs SET graduate = FALSE WHERE id = $1", agentOnly)

	// Runs of another workspace's job never show up
	other := testutil.SeedWorkspace(t, db)
	otherJob := testutil.SeedJob(t, db, other, ConcurrencySkip)
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at)
		VALUES ($1, $2, $3, 1, 'succeeded', 'explore', 'manual', 0, 1)`, database.NewID(), other, otherJob)

	out, err := m.list(ctx, &listInput{ListParams: httpserver.ListParams{Page: 1, PageSize: 25}})
	require.NoError(t, err)
	items := out.Body.Items
	require.Len(t, items, 5)
	byName := map[string]JobListDto{}
	for _, d := range items {
		byName[d.Name] = d
	}

	// The strip holds the newest ten runs, newest first, and the last run is the first of them
	b := byName["a busy"]
	require.Len(t, b.RecentRuns, recentRunsPerJob)
	for i, r := range b.RecentRuns {
		assert.Equal(t, int64(12-i), r.Number)
	}
	assert.Equal(t, "failed", b.RecentRuns[0].Status)
	require.NotNil(t, b.LastRun)
	assert.Equal(t, b.RecentRuns[0], *b.LastRun)
	require.NotNil(t, b.LastMode)
	assert.Equal(t, ModeExplore, *b.LastMode)
	assert.Equal(t, ModeExplore, b.NextMode)
	assert.Equal(t, []IOField{{Name: "url", Type: "string", Description: "The page to check"}}, b.Inputs)

	// A job without runs has an empty strip that encodes as a list
	f := byName["b fresh"]
	assert.Nil(t, f.LastRun)
	assert.Nil(t, f.LastMode)
	encoded, err := json.Marshal(f)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"recentRuns":[]`)
	assert.Contains(t, string(encoded), `"inputs":[]`)
	assert.Equal(t, ModeAssisted, f.NextMode)

	assert.Equal(t, ModeAssisted, byName["c demoted"].NextMode)
	assert.Equal(t, ModeScripted, byName["d scripted"].NextMode)
	assert.Equal(t, ModeAssisted, byName["e agent only"].NextMode)
	assert.False(t, byName["e agent only"].Graduate)

	// Every job's next mode matches the one the job page shows
	for _, d := range items {
		job, err := m.getJob(ctx, wid, d.ID)
		require.NoError(t, err)
		want, _, err := m.nextMode(ctx, job)
		require.NoError(t, err)
		assert.Equal(t, want, d.NextMode, d.Name)
	}
}

func TestListSearchFoldsNonASCIICapitals(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	id := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	testutil.Exec(t, db, "UPDATE jobs SET name = $1 WHERE id = $2", "Überweisungen prüfen", id)

	// A name that starts with a non-ASCII capital is found by its exact name and in any case, like on Postgres
	for _, term := range []string{"prüfen", "Überweisungen", "überweisungen", "ÜBERWEISUNGEN", "Überweisungen prüfen"} {
		out, err := m.list(ctx, &listInput{ListParams: httpserver.ListParams{Page: 1, PageSize: 25, Search: term}})
		require.NoError(t, err)
		require.Len(t, out.Body.Items, 1, "engine %s, search %q", db.Engine(), term)
	}
}
