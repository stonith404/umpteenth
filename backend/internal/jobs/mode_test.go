//go:build unit

package jobs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestNextModeFollowsGraduationAndDemotion(t *testing.T) {
	m, _, db := newTestModule(t)
	ctx := context.Background()
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	pb := m.deps.Playbooks.(*playbook.Module)

	mode := func() (string, bool) {
		t.Helper()
		job, err := m.getJob(ctx, wid, jobID)
		require.NoError(t, err)
		mode, demoted, err := m.nextMode(ctx, job)
		require.NoError(t, err)
		return mode, demoted
	}
	number := int64(0)
	scriptedRun := func(version int64, fellBack bool) {
		number++
		testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, fell_back, trigger, playbook_version, queued_at)
			VALUES ($1, $2, $3, $4, 'succeeded', 'scripted', $5, 'manual', $6, $7)`, database.NewID(), wid, jobID, number, fellBack, version, database.Now())
	}
	save := func(c playbook.Content, graduated bool) int64 {
		v, err := pb.Save(ctx, wid, jobID, c, playbook.VersionMeta{Author: playbook.AuthorReflection, Graduated: graduated})
		require.NoError(t, err)
		return v
	}

	// An empty playbook explores, know-how assists
	got, _ := mode()
	assert.Equal(t, ModeExplore, got)
	learning := []playbook.Learning{{ID: "L1", Kind: "fact", Text: "x", Status: "active"}}
	save(playbook.Content{Learnings: learning}, false)
	got, _ = mode()
	assert.Equal(t, ModeAssisted, got)

	// A main script graduates the job
	main := "#!/bin/sh\necho ok\n"
	v2 := save(playbook.Content{Learnings: learning, Main: &main}, false)
	got, _ = mode()
	assert.Equal(t, ModeScripted, got)

	// One fallback keeps it scripted, and a repair doesn't reset the count
	scriptedRun(v2, true)
	repaired := "#!/bin/sh\necho fixed\n"
	v3 := save(playbook.Content{Learnings: learning, Main: &repaired}, false)
	got, _ = mode()
	assert.Equal(t, ModeScripted, got)

	// A second fallback in a row demotes it to Assisted, until it graduates again
	scriptedRun(v3, true)
	got, demoted := mode()
	assert.Equal(t, ModeAssisted, got)
	assert.True(t, demoted)
	save(playbook.Content{Learnings: learning, Main: &repaired}, true)
	got, demoted = mode()
	assert.Equal(t, ModeScripted, got)
	assert.False(t, demoted)

	// Pins win over the playbook
	pin := func(p string) {
		testutil.Exec(t, db, "UPDATE jobs SET mode_pin = $1 WHERE id = $2", p, jobID)
	}
	pin(ModeAssisted)
	got, _ = mode()
	assert.Equal(t, ModeAssisted, got)
	pin(ModeExplore)
	got, _ = mode()
	assert.Equal(t, ModeExplore, got)
}
