package playbook

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/playbook/playbookdb"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// Version authors
const (
	AuthorUser       = "user"
	AuthorReflection = "reflection"
	AuthorRollback   = "rollback"
	AuthorCompile    = "compile"
)

// JobChecker verifies a job belongs to the workspace, since playbook versions are scoped through their job
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

// ImageBuilds starts a build when a version brings a new Dockerfile
type ImageBuilds interface {
	EnsureBuild(ctx context.Context, jobID, dockerfile string) error
}

type Dependencies struct {
	DB     *database.DB
	Jobs   JobChecker
	Images ImageBuilds
}

type Module struct {
	deps    Dependencies
	db      *database.DB
	queries *playbookdb.Queries
}

func New(deps Dependencies) *Module {
	return &Module{deps: deps, db: deps.DB, queries: playbookdb.New(deps.DB)}
}

// SetDependencies wires the collaborators built after this module
func (m *Module) SetDependencies(jobs JobChecker, images ImageBuilds) {
	m.deps.Jobs = jobs
	m.deps.Images = images
}

// Content returns a playbook version; version 0 is the empty playbook of a job that has not learned anything
func (m *Module) Content(ctx context.Context, jobID string, version int64) (Content, error) {
	_, c, err := m.loadVersion(ctx, jobID, version)
	return c, err
}

// loadVersion returns a stored version with its content, and no row for version 0
func (m *Module) loadVersion(ctx context.Context, jobID string, version int64) (*playbookdb.PlaybookVersion, Content, error) {
	var c Content
	if version == 0 {
		c.Normalize()
		return nil, c, nil
	}
	row, err := m.queries.GetVersion(ctx, playbookdb.GetVersionParams{JobID: jobID, Version: version})
	if database.IsNotFound(err) {
		return nil, c, apperror.NotFound("Playbook version")
	} else if err != nil {
		return nil, c, fmt.Errorf("failed to load playbook: %w", err)
	}
	err = json.Unmarshal([]byte(row.Content), &c)
	if err != nil {
		return nil, c, fmt.Errorf("invalid playbook content: %w", err)
	}
	c.Normalize()
	return &row, c, nil
}

// Current returns the job's current version with its live usage counters
func (m *Module) Current(ctx context.Context, jobID string) (int64, Content, error) {
	v, err := m.queries.CurrentVersion(ctx, jobID)
	if err != nil {
		return 0, Content{}, fmt.Errorf("failed to load the playbook version: %w", err)
	}
	c, err := m.Content(ctx, jobID, v)
	if err != nil {
		return 0, Content{}, err
	}
	err = m.withStats(ctx, jobID, &c)
	return v, c, err
}

// Stat kinds in playbook_stats
const (
	statLearning = "learning"
	statScript   = "script"
)

// RecordStats adds one run's usage of learnings and toolkit scripts to the job's counters
func (m *Module) RecordStats(ctx context.Context, jobID string, hits []string, scripts map[string]ScriptStats) error {
	now := database.Now()
	for _, id := range hits {
		err := m.queries.AddStats(ctx, playbookdb.AddStatsParams{JobID: jobID, Kind: statLearning, Name: id, Hits: 1, UpdatedAt: now})
		if err != nil {
			return fmt.Errorf("failed to record learning hits: %w", err)
		}
	}
	for name, st := range scripts {
		err := m.queries.AddStats(ctx, playbookdb.AddStatsParams{JobID: jobID, Kind: statScript, Name: name, Calls: int64(st.Calls), Failures: int64(st.Failures), UpdatedAt: now})
		if err != nil {
			return fmt.Errorf("failed to record toolkit stats: %w", err)
		}
	}
	return nil
}

// withStats replaces the usage counters stored with a version by the live ones, which change with every run
func (m *Module) withStats(ctx context.Context, jobID string, c *Content) error {
	rows, err := m.queries.ListStats(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to load playbook stats: %w", err)
	}
	for _, row := range rows {
		switch row.Kind {
		case statLearning:
			for i := range c.Learnings {
				if c.Learnings[i].ID == row.Name {
					c.Learnings[i].Hits = int(row.Hits)
				}
			}
		case statScript:
			for i := range c.Toolkit {
				if c.Toolkit[i].Name == row.Name {
					c.Toolkit[i].Stats = ScriptStats{Calls: int(row.Calls), Failures: int(row.Failures)}
				}
			}
		}
	}
	return nil
}

// VersionMeta describes who created a version and why
type VersionMeta struct {
	Author      string
	UserID      *string
	SourceRunID *string
	Summary     string
	Ops         json.RawMessage
	// BaseVersion is the version an edit started from; when set, the save is refused if a newer version was written meanwhile
	BaseVersion *int64
	// Graduated marks a version that makes the job scripted again, e.g. reflection's propose_main after a demotion
	Graduated bool
}

// Save stores content as the next version and makes it the job's current playbook
// Every change is a new version, so history is never rewritten
func (m *Module) Save(ctx context.Context, workspaceID, jobID string, c Content, meta VersionMeta) (int64, error) {
	// Headers are synced into a copy, since the caller's toolkit shares its backing array with c
	c = c.clone()
	err := c.Validate()
	if err != nil {
		return 0, err
	}
	err = c.syncScriptHeaders()
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return 0, err
	}

	var version int64
	err = m.db.InTx(ctx, func(tx *database.Tx) error {
		// Taking the next number locks the job's row, so concurrent saves of one job take turns instead of racing for it
		q := playbookdb.New(tx)
		var err error
		version, err = q.NextVersion(ctx, playbookdb.NextVersionParams{WorkspaceID: workspaceID, JobID: jobID, UpdatedAt: database.Now()})
		if database.IsNotFound(err) {
			return apperror.NotFound("Job")
		} else if err != nil {
			return err
		}

		// Saving over a newer version, e.g. one reflection wrote while the page was open, would silently drop its changes
		if meta.BaseVersion != nil && *meta.BaseVersion != version-1 {
			return apperror.Conflict("The playbook changed since it was loaded, reload to see the latest version")
		}

		params := playbookdb.InsertVersionParams{
			JobID: jobID, Version: version, Content: string(raw), Author: meta.Author, AuthorUserID: meta.UserID, SourceRunID: meta.SourceRunID, CreatedAt: database.Now(),
		}
		if len(meta.Ops) > 0 {
			params.Ops = new(string(meta.Ops))
		}
		if meta.Summary != "" {
			params.Summary = &meta.Summary
		}
		if c.Dockerfile != nil {
			params.DockerfileHash = new(HashDockerfile(*c.Dockerfile))
		}
		err = q.InsertVersion(ctx, params)
		if err != nil {
			return err
		}

		// A version that brings a main script starts a new graduation, so fallbacks of an older main no longer count towards demotion
		graduated := meta.Graduated
		if !graduated && c.Main != nil {
			graduated = version == 1
			if version > 1 {
				prev, err := q.GetVersion(ctx, playbookdb.GetVersionParams{JobID: jobID, Version: version - 1})
				if err != nil {
					return err
				}
				var before Content
				graduated = json.Unmarshal([]byte(prev.Content), &before) != nil || before.Main == nil
			}
		}
		if graduated {
			return q.SetGraduatedVersion(ctx, playbookdb.SetGraduatedVersionParams{WorkspaceID: workspaceID, JobID: jobID, Version: &version})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	// A new Dockerfile needs an image before the next run can use it
	// The version is already committed, so a failure to queue the build is only logged: the next run queues it again
	if c.Dockerfile != nil && m.deps.Images != nil {
		err = m.deps.Images.EnsureBuild(ctx, jobID, *c.Dockerfile)
		if err != nil {
			slog.WarnContext(ctx, "Failed to start the image build for a playbook version", slog.String("job", jobID), slog.Int64("version", version), slog.Any("error", err))
		}
	}
	return version, nil
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("get-playbook", http.MethodGet, "/api/jobs/{id}/playbook", "Playbook"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("update-playbook", http.MethodPut, "/api/jobs/{id}/playbook", "Playbook"), auth, m.update)
	httpserver.Register(api, httpserver.Operation("list-playbook-versions", http.MethodGet, "/api/jobs/{id}/playbook/versions", "Playbook"), auth, m.listVersions)
	httpserver.Register(api, httpserver.Operation("get-playbook-version", http.MethodGet, "/api/jobs/{id}/playbook/versions/{version}", "Playbook"), auth, m.getVersion)
	httpserver.Register(api, httpserver.Operation("rollback-playbook", http.MethodPost, "/api/jobs/{id}/playbook/rollback", "Playbook"), auth, m.rollback)
}

type versionDto struct {
	Version     int64   `json:"version"`
	Author      string  `json:"author"`
	Summary     *string `json:"summary"`
	SourceRunID *string `json:"sourceRunId"`
	CreatedAt   int64   `json:"createdAt"`
	// Ops are the operations reflection proposed for this version, including held and rejected ones
	Ops     []AppliedOp `json:"ops,omitempty"`
	Content Content     `json:"content"`
	// Previous is the content of the version before, for diffs
	Previous *Content `json:"previous,omitempty"`
}

type jobInput struct {
	ID string `path:"id"`
}

type playbookOutput struct {
	Body versionDto
}

func (m *Module) get(ctx context.Context, in *jobInput) (*playbookOutput, error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	v, err := m.queries.CurrentVersion(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out, err := m.versionOutput(ctx, in.ID, v, false)
	if err != nil {
		return nil, err
	}
	err = m.withStats(ctx, in.ID, &out.Body.Content)
	return out, err
}

func (m *Module) versionOutput(ctx context.Context, jobID string, v int64, withPrevious bool) (*playbookOutput, error) {
	row, c, err := m.loadVersion(ctx, jobID, v)
	if err != nil {
		return nil, err
	}
	out := &playbookOutput{Body: versionDto{Version: v, Author: AuthorUser, Content: c}}
	if row == nil {
		return out, nil
	}
	out.Body.Author, out.Body.Summary, out.Body.SourceRunID, out.Body.CreatedAt = row.Author, row.Summary, row.SourceRunID, row.CreatedAt
	if row.Ops != nil {
		_ = json.Unmarshal([]byte(*row.Ops), &out.Body.Ops)
	}
	if withPrevious {
		_, prev, err := m.loadVersion(ctx, jobID, v-1)
		if err != nil {
			return nil, err
		}
		out.Body.Previous = &prev
	}
	return out, nil
}

type updateInput struct {
	ID   string `path:"id"`
	Body struct {
		Content Content `json:"content"`
		Summary string  `json:"summary,omitempty" maxLength:"500"`
		// BaseVersion guards against overwriting a newer version, and is optional so scripts can still write unconditionally
		BaseVersion *int64 `json:"baseVersion,omitempty" doc:"The version the edit started from; a newer current version makes the save fail with 409"`
	}
}

func (m *Module) update(ctx context.Context, in *updateInput) (*playbookOutput, error) {
	wid := principal.WorkspaceID(ctx)
	err := m.deps.Jobs.JobExists(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	summary := in.Body.Summary
	if summary == "" {
		summary = "Edited by hand"
	}
	v, err := m.Save(ctx, wid, in.ID, in.Body.Content, VersionMeta{Author: AuthorUser, UserID: principal.UserIDPtr(ctx), Summary: summary, BaseVersion: in.Body.BaseVersion})
	if err != nil {
		return nil, err
	}
	return m.versionOutput(ctx, in.ID, v, false)
}

type listVersionsInput struct {
	ID string `path:"id"`
	httpserver.ListParams
}

type versionListDto struct {
	Version     int64   `json:"version"`
	Author      string  `json:"author"`
	Summary     *string `json:"summary"`
	SourceRunID *string `json:"sourceRunId"`
	CreatedAt   int64   `json:"createdAt"`
}

var versionsSpec = &listquery.Spec{
	Select:      "SELECT version, author, summary, source_run_id, created_at FROM playbook_versions",
	From:        "FROM playbook_versions",
	Sorts:       map[string]string{"version": "version", "createdAt": "created_at"},
	DefaultSort: "-version",
	Search:      []string{"summary", "author"},
	TieBreaker:  "version",
}

func (m *Module) listVersions(ctx context.Context, in *listVersionsInput) (*httpserver.PaginatedOutput[versionListDto], error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	q := listquery.New(versionsSpec).WhereEq("job_id", in.ID)
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (versionListDto, error) {
		var d versionListDto
		err := rows.Scan(&d.Version, &d.Author, &d.Summary, &d.SourceRunID, &d.CreatedAt)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type versionInput struct {
	ID      string `path:"id"`
	Version int64  `path:"version"`
}

func (m *Module) getVersion(ctx context.Context, in *versionInput) (*playbookOutput, error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	return m.versionOutput(ctx, in.ID, in.Version, true)
}

type rollbackInput struct {
	ID   string `path:"id"`
	Body struct {
		Version int64 `json:"version" minimum:"0"`
	}
}

// rollback creates a new version with the content of an older one, so history stays append-only
func (m *Module) rollback(ctx context.Context, in *rollbackInput) (*playbookOutput, error) {
	wid := principal.WorkspaceID(ctx)
	err := m.deps.Jobs.JobExists(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	c, err := m.Content(ctx, in.ID, in.Body.Version)
	if err != nil {
		return nil, err
	}
	v, err := m.Save(ctx, wid, in.ID, c, VersionMeta{Author: AuthorRollback, UserID: principal.UserIDPtr(ctx), Summary: fmt.Sprintf("Rolled back to version %d", in.Body.Version)})
	if err != nil {
		return nil, err
	}
	return m.versionOutput(ctx, in.ID, v, false)
}
