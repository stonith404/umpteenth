//go:build unit

package skills

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/skills/skillsdb"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// jobChecker accepts the jobs seeded for the test's workspace
type jobChecker struct{ db *database.DB }

func (j jobChecker) JobExists(ctx context.Context, workspaceID, jobID string) error {
	var n int
	err := j.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE workspace_id = $1 AND id = $2", workspaceID, jobID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperror.NotFound("Job")
	}
	return nil
}

type harness struct {
	m       *Module
	db      *database.DB
	storage storage.FileStorage
	ctx     context.Context
	wid     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testutil.NewDatabaseForTest(t)
	fsStorage, err := storage.NewFilesystemStorage(t.TempDir())
	require.NoError(t, err)
	wid := testutil.SeedWorkspace(t, db)
	m := New(Dependencies{DB: db, Storage: fsStorage, Jobs: jobChecker{db}})
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{UserID: "u1", WorkspaceID: wid})
	return &harness{m: m, db: db, storage: fsStorage, ctx: ctx, wid: wid}
}

func skillZip(t *testing.T, name, description string, extra ...entry) []byte {
	t.Helper()
	entries := append([]entry{{name: "SKILL.md", content: fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nBody\n", name, description)}}, extra...)
	return makeZip(t, entries...)
}

func (h *harness) create(t *testing.T, body []byte) SkillDto {
	t.Helper()
	out, err := h.m.create(h.ctx, &createInput{RawBody: body})
	require.NoError(t, err)
	return out.Body
}

// blobs lists the stored keys of a skill
func (h *harness) blobs(t *testing.T, id string) []string {
	t.Helper()
	objects, err := h.storage.List(h.ctx, "skills/"+id)
	require.NoError(t, err)
	keys := make([]string, len(objects))
	for i, o := range objects {
		keys[i] = o.Key
	}
	return keys
}

func TestCreateStoresTheSkill(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "Says hello", entry{name: "scripts/hello.sh", content: "echo hi", mode: 0o755}))
	require.Equal(t, "demo", s.Name)
	require.Equal(t, "Says hello", s.Description)
	require.Equal(t, int64(2), s.FileCount)
	require.Equal(t, []SkillFile{{Path: "SKILL.md", Size: s.Files[0].Size}, {Path: "scripts/hello.sh", Size: 7, Executable: true}}, s.Files)
	require.Equal(t, []string{blobKey(s.ID, s.ContentHash)}, h.blobs(t, s.ID))

	// A second skill with the same name is refused and leaves no blob behind
	_, err := h.m.create(h.ctx, &createInput{RawBody: skillZip(t, "demo", "Another")})
	require.True(t, apperror.IsCode(err, apperror.CodeAlreadyInUse), err)
	all, err := h.storage.List(h.ctx, "skills")
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestReplaceKeepsTheNameAndSwapsTheBlob(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "First"))

	// Another name is a different skill
	_, err := h.m.replace(h.ctx, &replaceInput{ID: s.ID, RawBody: skillZip(t, "other", "First")})
	requireInvalid(t, err, "name_changed")

	// The same content changes nothing
	out, err := h.m.replace(h.ctx, &replaceInput{ID: s.ID, RawBody: skillZip(t, "demo", "First")})
	require.NoError(t, err)
	require.Equal(t, s.UpdatedAt, out.Body.UpdatedAt)

	// New content replaces the stored version, and the old blob goes
	out, err = h.m.replace(h.ctx, &replaceInput{ID: s.ID, RawBody: skillZip(t, "demo", "Second")})
	require.NoError(t, err)
	require.Equal(t, "Second", out.Body.Description)
	require.NotEqual(t, s.ContentHash, out.Body.ContentHash)
	require.Equal(t, []string{blobKey(s.ID, out.Body.ContentHash)}, h.blobs(t, s.ID))
}

func TestReplaceRefusesAStaleVersion(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "First"))

	// Another upload landed after this one read the skill
	n, err := h.m.queries.ReplaceSkill(h.ctx, skillsdb.ReplaceSkillParams{
		Description: "Meanwhile", ContentHash: "other", Files: "[]", WorkspaceID: h.wid, ID: s.ID, OldHash: "not-the-current-hash",
	})
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestDeleteRemovesTheSkillEverywhere(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "Says hello"))
	jobID := testutil.SeedJob(t, h.db, h.wid, "skip")
	_, err := h.m.setJobSkills(h.ctx, &setJobSkillsInput{ID: jobID, Body: []JobSkill{{SkillID: s.ID}}})
	require.NoError(t, err)

	got, err := h.m.get(h.ctx, &idInput{ID: s.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Body.JobCount)

	_, err = h.m.delete(h.ctx, &idInput{ID: s.ID})
	require.NoError(t, err)
	require.Empty(t, h.blobs(t, s.ID))
	jobSkills, err := h.m.getJobSkills(h.ctx, &idInput{ID: jobID})
	require.NoError(t, err)
	require.Empty(t, jobSkills.Body)

	_, err = h.m.get(h.ctx, &idInput{ID: s.ID})
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound), err)
}

func TestJobSkillsStayInTheirWorkspace(t *testing.T) {
	h := newHarness(t)
	jobID := testutil.SeedJob(t, h.db, h.wid, "skip")

	// A skill of another workspace can't be attached
	otherWid := testutil.SeedWorkspace(t, h.db)
	other := &harness{m: h.m, db: h.db, storage: h.storage, wid: otherWid, ctx: principal.WithPrincipal(t.Context(), principal.Principal{UserID: "u2", WorkspaceID: otherWid})}
	foreign := other.create(t, skillZip(t, "foreign", "Not yours"))
	_, err := h.m.setJobSkills(h.ctx, &setJobSkillsInput{ID: jobID, Body: []JobSkill{{SkillID: foreign.ID}}})
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound), err)

	// Attached skills come back by name, which keeps the prompt stable
	b := h.create(t, skillZip(t, "beta", "B"))
	a := h.create(t, skillZip(t, "alpha", "A"))
	out, err := h.m.setJobSkills(h.ctx, &setJobSkillsInput{ID: jobID, Body: []JobSkill{{SkillID: b.ID}, {SkillID: a.ID}, {SkillID: b.ID}}})
	require.NoError(t, err)
	require.Equal(t, []JobSkill{{SkillID: a.ID, SkillName: "alpha"}, {SkillID: b.ID, SkillName: "beta"}}, out.Body)

	loaded, err := h.m.JobSkills(h.ctx, h.wid, jobID)
	require.NoError(t, err)
	require.Equal(t, []runner.Skill{{ID: a.ID, Name: "alpha", Description: "A", Size: a.Size}, {ID: b.ID, Name: "beta", Description: "B", Size: b.Size}}, loaded)

	// The other workspace's job is out of reach as well
	_, err = other.m.getJobSkills(other.ctx, &idInput{ID: jobID})
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound), err)
}

func TestJobSkillsAreLimited(t *testing.T) {
	h := newHarness(t)
	jobID := testutil.SeedJob(t, h.db, h.wid, "skip")
	var body []JobSkill
	for i := range runner.MaxJobSkills + 1 {
		s := h.create(t, skillZip(t, fmt.Sprintf("skill-%d", i), "Does a thing"))
		body = append(body, JobSkill{SkillID: s.ID})
	}
	_, err := h.m.setJobSkills(h.ctx, &setJobSkillsInput{ID: jobID, Body: body})
	requireInvalidField(t, err, "body", "too_many_skills")

	// A failed attach changes nothing
	out, err := h.m.getJobSkills(h.ctx, &idInput{ID: jobID})
	require.NoError(t, err)
	require.Empty(t, out.Body)
}

func TestSkillFilesReadTheCurrentVersion(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "First", entry{name: "scripts/run.sh", content: "#!/bin/sh\necho 1"}))
	_, err := h.m.replace(h.ctx, &replaceInput{ID: s.ID, RawBody: skillZip(t, "demo", "Second", entry{name: "scripts/run.sh", content: "#!/bin/sh\necho 2"})})
	require.NoError(t, err)

	// The skill as the job was loaded still finds the version that replaced it
	files, err := h.m.SkillFiles(h.ctx, h.wid, runner.Skill{ID: s.ID, Name: "demo"})
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "SKILL.md", files[0].Path)
	require.Contains(t, string(files[0].Content), "Second")
	require.Equal(t, "scripts/run.sh", files[1].Path)
	require.Equal(t, fs.FileMode(0o755), files[1].Mode)
	require.Equal(t, "#!/bin/sh\necho 2", string(files[1].Content))
}

func TestReleaseWorkspaceDeletesTheBlobs(t *testing.T) {
	h := newHarness(t)
	s := h.create(t, skillZip(t, "demo", "First"))
	after, err := h.m.ReleaseWorkspace(h.ctx, h.wid)
	require.NoError(t, err)
	after(h.ctx)
	require.Empty(t, h.blobs(t, s.ID))
}

func TestCatalogListsTheWorkspaceSkills(t *testing.T) {
	h := newHarness(t)
	h.create(t, skillZip(t, "zeta", "Z"))
	h.create(t, skillZip(t, "alpha", "A"))
	catalog, err := h.m.Skills(h.ctx, h.wid)
	require.NoError(t, err)
	names := make([]string, len(catalog))
	for i, s := range catalog {
		names[i] = s.Name
	}
	require.Equal(t, "alpha,zeta", strings.Join(names, ","))
}

func requireInvalidField(t *testing.T, err error, field, code string) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), field)
	appErr, ok := apperror.As(err)
	require.True(t, ok, err)
	require.True(t, apperror.IsCode(appErr, apperror.CodeValidationFailed), err)
	raw, _ := appErr.MarshalJSON()
	require.Contains(t, string(raw), `"code":"`+code+`"`)
}

func TestDeleteManyReportsWhatItSkipped(t *testing.T) {
	h := newHarness(t)
	a := h.create(t, skillZip(t, "alpha", "A"))
	b := h.create(t, skillZip(t, "beta", "B"))
	keep := h.create(t, skillZip(t, "gamma", "C"))
	jobID := testutil.SeedJob(t, h.db, h.wid, "skip")
	_, err := h.m.setJobSkills(h.ctx, &setJobSkillsInput{ID: jobID, Body: []JobSkill{{SkillID: a.ID}, {SkillID: keep.ID}}})
	require.NoError(t, err)

	// A skill of another workspace counts as missing, like one deleted meanwhile
	otherWid := testutil.SeedWorkspace(t, h.db)
	other := &harness{m: h.m, db: h.db, storage: h.storage, wid: otherWid, ctx: principal.WithPrincipal(t.Context(), principal.Principal{UserID: "u2", WorkspaceID: otherWid})}
	foreign := other.create(t, skillZip(t, "foreign", "Not yours"))

	in := &deleteManyInput{}
	in.Body.IDs = []string{a.ID, b.ID, "missing", foreign.ID}
	out, err := h.m.deleteMany(h.ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{a.ID, b.ID}, out.Body.Deleted)
	require.Equal(t, []string{"missing", foreign.ID}, out.Body.Skipped)

	// The deleted skills lose their files and their jobs, and the others stay
	require.Empty(t, h.blobs(t, a.ID))
	require.Empty(t, h.blobs(t, b.ID))
	require.NotEmpty(t, other.blobs(t, foreign.ID))
	jobSkills, err := h.m.getJobSkills(h.ctx, &idInput{ID: jobID})
	require.NoError(t, err)
	require.Equal(t, []JobSkill{{SkillID: keep.ID, SkillName: "gamma"}}, jobSkills.Body)
}
