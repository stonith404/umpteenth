package skills

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/skills/skillsdb"
)

// Skills lists the workspace's skills by name, used by the compile step to suggest fitting ones
func (m *Module) Skills(ctx context.Context, workspaceID string) ([]runner.Skill, error) {
	rows, err := m.queries.ListSkillCatalog(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]runner.Skill, len(rows))
	for i, r := range rows {
		out[i] = runner.Skill{ID: r.ID, Name: r.Name, Description: r.Description, Size: r.Size}
	}
	return out, nil
}

// JobSkills lists a job's skills by name, which keeps the system prompt byte-stable between runs
func (m *Module) JobSkills(ctx context.Context, workspaceID, jobID string) ([]runner.Skill, error) {
	rows, err := m.queries.ListJobSkills(ctx, skillsdb.ListJobSkillsParams{WorkspaceID: workspaceID, JobID: jobID})
	if err != nil {
		return nil, err
	}
	out := make([]runner.Skill, len(rows))
	for i, r := range rows {
		out[i] = runner.Skill{ID: r.ID, Name: r.Name, Description: r.Description, Size: r.Size}
	}
	return out, nil
}

// SkillFiles implements runner.SkillFiles, reading the skill's current version
// The version is looked up again rather than taken from when the job was loaded, since a replace in between deletes the old blob
func (m *Module) SkillFiles(ctx context.Context, workspaceID string, skill runner.Skill) ([]sandbox.File, error) {
	s, err := m.getSkill(ctx, workspaceID, skill.ID)
	if err != nil {
		return nil, err
	}
	a, err := m.load(ctx, s)
	if err != nil {
		return nil, err
	}
	files := make([]sandbox.File, len(a.Files))
	for i, f := range a.Files {
		files[i] = sandbox.File{Path: f.Path, Mode: f.Mode, Content: f.Content}
	}
	return files, nil
}

// ReleaseWorkspace returns a function that deletes the stored skills of a workspace once the rows of the deleted workspace are gone
func (m *Module) ReleaseWorkspace(ctx context.Context, workspaceID string) (func(context.Context), error) {
	ids, err := m.queries.ListSkillIDsOfWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	return func(ctx context.Context) {
		for _, id := range ids {
			if err := m.deps.Storage.DeleteAll(ctx, "skills/"+id); err != nil {
				slog.WarnContext(ctx, "Failed to delete the files of a deleted workspace's skill", slog.String("skill", id), slog.Any("error", err))
			}
		}
	}, nil
}
