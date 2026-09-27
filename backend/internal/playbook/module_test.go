//go:build unit

package playbook_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestValidScriptName(t *testing.T) {
	for _, name := range []string{"fetch.sh", "a", "Report_2-final.py", strings.Repeat("a", 64)} {
		assert.True(t, playbook.ValidScriptName(name), name)
	}
	for _, name := range []string{"", "../../usr/local/bin/ump", "sub/dir.sh", `a\b`, ".hidden", "-flag", "a..b", "..", strings.Repeat("a", 65), "a b"} {
		assert.False(t, playbook.ValidScriptName(name), name)
	}
}

func TestContentValidateRejectsUnsafeAndDuplicateNames(t *testing.T) {
	var appErr *apperror.Error

	err := playbook.Content{Toolkit: []playbook.Script{{Name: "ok.sh"}, {Name: "../../usr/local/bin/ump"}}}.Validate()
	require.ErrorAs(t, err, &appErr)
	assert.Contains(t, err.Error(), "content.toolkit[1].name")

	err = playbook.Content{Toolkit: []playbook.Script{{Name: "ok.sh"}, {Name: "ok.sh"}}}.Validate()
	require.ErrorAs(t, err, &appErr)
	assert.Contains(t, err.Error(), "another toolkit script")

	// Both would be offered as the tool toolkit__ok_sh
	err = playbook.Content{Toolkit: []playbook.Script{{Name: "ok.sh"}, {Name: "ok_sh"}}}.Validate()
	require.ErrorAs(t, err, &appErr)

	require.NoError(t, playbook.Content{Toolkit: []playbook.Script{{Name: "a.sh"}, {Name: "b.sh"}}}.Validate())
}

type failingImages struct{ calls int }

func (f *failingImages) EnsureBuild(context.Context, string, string) error {
	f.calls++
	return errors.New("taskpool unavailable")
}

func TestSaveRejectsAnEscapingToolkitName(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := playbook.New(playbook.Dependencies{DB: db})

	_, err := m.Save(context.Background(), wid, jobID, playbook.Content{Toolkit: []playbook.Script{{Name: "../../usr/local/bin/ump", Content: "#!/bin/sh"}}}, playbook.VersionMeta{Author: playbook.AuthorUser})
	var appErr *apperror.Error
	require.ErrorAs(t, err, &appErr)

	// Nothing was stored for the rejected content
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM playbook_versions WHERE job_id = $1", jobID).Scan(&count))
	assert.Zero(t, count)
}

func TestSaveSucceedsWhenTheImageBuildCannotBeQueued(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	images := &failingImages{}
	m := playbook.New(playbook.Dependencies{DB: db, Images: images})

	// The version is committed before the build is queued, so the request must not report a failure for it
	dockerfile := "FROM debian:trixie-slim\n"
	v, err := m.Save(context.Background(), wid, jobID, playbook.Content{Dockerfile: &dockerfile}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)
	assert.EqualValues(t, 1, v)
	assert.Equal(t, 1, images.calls)

	c, err := m.Content(context.Background(), jobID, v)
	require.NoError(t, err)
	require.NotNil(t, c.Dockerfile)
	assert.Equal(t, dockerfile, *c.Dockerfile)
}

func TestSaveRefusesToOverwriteANewerVersion(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := playbook.New(playbook.Dependencies{DB: db})
	ctx := context.Background()

	// Reflection writes version 1 while the page still shows the empty version 0
	v, err := m.Save(ctx, wid, jobID, playbook.Content{}, playbook.VersionMeta{Author: playbook.AuthorUser, Summary: "reflection"})
	require.NoError(t, err)
	require.EqualValues(t, 1, v)

	// An edit based on version 0 is refused instead of dropping version 1
	_, err = m.Save(ctx, wid, jobID, playbook.Content{}, playbook.VersionMeta{Author: playbook.AuthorUser, BaseVersion: new(int64(0))})
	require.True(t, apperror.IsCode(err, apperror.CodeConflict), err)

	// An edit based on the current version goes through
	v, err = m.Save(ctx, wid, jobID, playbook.Content{}, playbook.VersionMeta{Author: playbook.AuthorUser, BaseVersion: new(int64(1))})
	require.NoError(t, err)
	require.EqualValues(t, 2, v)

	// A job of another workspace is not found, and nothing is stored for it
	_, err = m.Save(ctx, testutil.SeedWorkspace(t, db), jobID, playbook.Content{}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound), err)
	current, _, err := m.Current(ctx, jobID)
	require.NoError(t, err)
	require.EqualValues(t, 2, current)
}

func TestConcurrentSavesTakeTurns(t *testing.T) {
	// A database file like in production, since the shared in-memory database of other tests fails concurrent transactions instead of making them wait
	db, err := database.Open(t.Context(), database.EngineSQLite, filepath.Join(t.TempDir(), "umpteenth.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.Migrate(t.Context(), db))
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := playbook.New(playbook.Dependencies{DB: db})

	// Every save gets a version of its own, and the job ends on the newest one
	var wg sync.WaitGroup
	versions := make([]int64, 5)
	for i := range versions {
		wg.Go(func() {
			v, err := m.Save(t.Context(), wid, jobID, playbook.Content{}, playbook.VersionMeta{Author: playbook.AuthorUser})
			assert.NoError(t, err)
			versions[i] = v
		})
	}
	wg.Wait()
	slices.Sort(versions)
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, versions)
	current, _, err := m.Current(t.Context(), jobID)
	require.NoError(t, err)
	assert.EqualValues(t, 5, current)
}

func TestSaveTakesScriptMetadataFromTheHeader(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := playbook.New(playbook.Dependencies{DB: db})

	// A script edited by hand declares new arguments in its header, which the stored tool definition follows
	script := "#!/usr/bin/env python3\n# ump:name report\n# ump:description Print the report\n# ump:args {\"days\":\"integer\"}\n# ump:side-effects external\nprint()\n"
	_, err := m.Save(ctx, wid, jobID, playbook.Content{Toolkit: []playbook.Script{{Name: "report", Description: "old", Content: script}}}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.NoError(t, err)
	_, c, err := m.Current(ctx, jobID)
	require.NoError(t, err)
	s := c.Toolkit[0]
	assert.Equal(t, "Print the report", s.Description)
	assert.JSONEq(t, `{"days":"integer"}`, string(s.Args))
	assert.True(t, s.SideEffects)
	assert.Equal(t, "python", s.Lang)

	// A header naming another script is rejected, since the tool name would silently differ from the code
	_, err = m.Save(ctx, wid, jobID, playbook.Content{Toolkit: []playbook.Script{{Name: "other", Content: script}}}, playbook.VersionMeta{Author: playbook.AuthorUser})
	require.ErrorContains(t, err, "ump:name header")
}
