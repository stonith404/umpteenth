// Package skills owns the agent skills of a workspace, uploaded as zips and attached to jobs
package skills

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/skills/skillsdb"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// uploadReadTimeout bounds reading an uploaded zip, which takes longer than a JSON body on a slow connection
const uploadReadTimeout = 60 * time.Second

// JobChecker verifies a job belongs to the workspace
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

// RateLimiter decides whether an import may download a skill
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

type Dependencies struct {
	DB      *database.DB
	Storage storage.FileStorage
	Jobs    JobChecker
	// Egress guards the downloads of skills imported from a link
	Egress *egress.Guard
	// ImportLimiter bounds the downloads per person and workspace, since a link can point at a large repository; nil admits every import
	ImportLimiter RateLimiter
}

type Module struct {
	deps    Dependencies
	db      *database.DB
	queries *skillsdb.Queries
	// githubArchives serves the tarballs of GitHub repositories, which tests point at a fake
	archivesMu     sync.RWMutex
	githubArchives string
}

func New(deps Dependencies) *Module {
	return &Module{deps: deps, db: deps.DB, queries: skillsdb.New(deps.DB), githubArchives: defaultGitHubArchives}
}

// SetJobs wires the job checker, which is built after this module
func (m *Module) SetJobs(j JobChecker) { m.deps.Jobs = j }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-skills", http.MethodGet, "/api/skills", "Skills"), auth, m.list)
	httpserver.Register(api, uploadOperation("create-skill", http.MethodPost, "/api/skills"), auth, m.create)
	httpserver.Register(api, httpserver.Operation("get-skill", http.MethodGet, "/api/skills/{id}", "Skills"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("import-skill", http.MethodPost, "/api/skills/import", "Skills"), auth, m.importSkill)
	httpserver.Register(api, httpserver.Operation("preview-skill-import", http.MethodPost, "/api/skills/import/preview", "Skills"), auth, m.previewImport)
	httpserver.Register(api, uploadOperation("replace-skill", http.MethodPut, "/api/skills/{id}"), auth, m.replace)
	httpserver.Register(api, httpserver.Operation("reimport-skill", http.MethodPost, "/api/skills/{id}/import", "Skills"), auth, m.reimportSkill)
	httpserver.Register(api, httpserver.Operation("delete-skill", http.MethodDelete, "/api/skills/{id}", "Skills"), auth, m.delete)
	httpserver.Register(api, httpserver.Operation("delete-skills", http.MethodPost, "/api/skills/delete", "Skills"), auth, m.deleteMany)
	httpserver.Register(api, httpserver.Operation("get-skill-file", http.MethodGet, "/api/skills/{id}/file", "Skills"), auth, m.getFile)
	httpserver.Register(api, httpserver.Operation("download-skill", http.MethodGet, "/api/skills/{id}/download", "Skills"), auth, m.download)
	httpserver.Register(api, httpserver.Operation("get-job-skills", http.MethodGet, "/api/jobs/{id}/skills", "Skills"), auth, m.getJobSkills)
	httpserver.Register(api, httpserver.Operation("set-job-skills", http.MethodPut, "/api/jobs/{id}/skills", "Skills"), auth, m.setJobSkills)
}

// uploadOperation takes a zip as the raw request body
// Huma only limits bodies it reads itself, and its multipart parsing spills large files to disk without a limit, so the zip is read as a raw body
func uploadOperation(id, method, p string) huma.Operation {
	op := httpserver.Operation(id, method, p, "Skills")
	op.MaxBodyBytes = maxArchiveBytes + 1
	op.BodyReadTimeout = uploadReadTimeout
	return op
}

// blobKey is where one version of a skill is stored, in its own folder so DeleteAll can remove exactly that version
func blobKey(skillID, hash string) string {
	return "skills/" + skillID + "/" + hash + "/skill.zip"
}

// SkillFile is one file of a skill
type SkillFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

// SkillDto is a skill as the API shows it
type SkillDto struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	ContentHash string      `json:"contentHash"`
	Files       []SkillFile `json:"files"`
	FileCount   int64       `json:"fileCount"`
	Size        int64       `json:"size" doc:"Unpacked size in bytes"`
	ArchiveSize int64       `json:"archiveSize" doc:"Size of the stored zip in bytes"`
	JobCount    int64       `json:"jobCount" doc:"How many jobs use the skill"`
	SourceURL   *string     `json:"sourceUrl" doc:"The link the skill was imported from, null for an uploaded skill"`
	CreatedAt   int64       `json:"createdAt"`
	UpdatedAt   int64       `json:"updatedAt"`
}

func toDto(s skillsdb.Skill, jobCount int64) SkillDto {
	d := SkillDto{
		ID: s.ID, Name: s.Name, Description: s.Description, ContentHash: s.ContentHash, Files: []SkillFile{}, FileCount: s.FileCount,
		Size: s.Size, ArchiveSize: s.ArchiveSize, JobCount: jobCount, SourceURL: s.SourceUrl, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	_ = json.Unmarshal([]byte(s.Files), &d.Files)
	return d
}

// manifest lists the files of a skill as the API shows them
func manifest(a *Archive) string {
	files := make([]SkillFile, len(a.Files))
	for i, f := range a.Files {
		files[i] = SkillFile{Path: f.Path, Size: int64(len(f.Content)), Executable: f.Executable()}
	}
	raw, _ := json.Marshal(files)
	return string(raw)
}

var listSpec = &listquery.Spec{
	Select: "SELECT id, workspace_id, name, description, content_hash, files, file_count, size, archive_size, source_url, created_at, updated_at, " +
		"(SELECT COUNT(*) FROM job_skills js WHERE js.skill_id = skills.id) AS job_count FROM skills",
	From:        "FROM skills",
	Sorts:       map[string]string{"name": "name", "size": "size", "createdAt": "created_at", "updatedAt": "updated_at"},
	DefaultSort: "name",
	Search:      []string{"name", "description"},
	TieBreaker:  "id",
}

type listInput struct {
	httpserver.ListParams
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[SkillDto], error) {
	q := listquery.New(listSpec).WhereEq("workspace_id", principal.WorkspaceID(ctx))
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (SkillDto, error) {
		var s skillsdb.Skill
		var jobCount int64
		err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Description, &s.ContentHash, &s.Files, &s.FileCount, &s.Size, &s.ArchiveSize, &s.SourceUrl, &s.CreatedAt, &s.UpdatedAt, &jobCount)
		return toDto(s, jobCount), err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type skillOutput struct {
	Body SkillDto
}

type createInput struct {
	RawBody []byte `contentType:"application/zip" doc:"A zip of the skill folder, with SKILL.md at its root or inside a single top-level folder"`
}

func (m *Module) create(ctx context.Context, in *createInput) (*skillOutput, error) {
	a, err := ParseArchive(in.RawBody)
	if err != nil {
		return nil, err
	}
	return m.add(ctx, principal.WorkspaceID(ctx), a, nil)
}

type importInput struct {
	Body struct {
		URL   string   `json:"url" minLength:"1" maxLength:"2000" doc:"A GitHub link to a skill's folder or its SKILL.md, or a link to a zip or .skill file"`
		Paths []string `json:"paths,omitempty" maxItems:"50" doc:"The skills to add from a GitHub folder of several, as folders relative to it like the preview lists them"`
	}
}

type importOutput struct {
	Body struct {
		Skills []SkillDto `json:"skills"`
	}
}

// importSkill adds the skill a link points at, or the picked skills of a GitHub folder of several
func (m *Module) importSkill(ctx context.Context, in *importInput) (*importOutput, error) {
	wid := principal.WorkspaceID(ctx)
	rawURL := strings.TrimSpace(in.Body.URL)
	err := m.allowImport(ctx, wid)
	if err != nil {
		return nil, err
	}
	out := &importOutput{}

	// A link to one skill adds it with the link as its source
	if len(in.Body.Paths) == 0 {
		a, err := m.importFrom(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		added, err := m.add(ctx, wid, a, &rawURL)
		if err != nil {
			return nil, err
		}
		out.Body.Skills = []SkillDto{added.Body}
		return out, nil
	}

	// Picked skills come from one download, and each keeps the link to its own folder
	dirs := []string{}
	for _, p := range in.Body.Paths {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if !slices.Contains(dirs, p) {
			dirs = append(dirs, p)
		}
	}
	picked, err := m.importPicked(ctx, rawURL, dirs)
	if err != nil {
		return nil, err
	}

	// Names are checked before anything is stored, so a taken name doesn't stop the pick halfway
	names := map[string]bool{}
	for _, p := range picked {
		if names[p.archive.Name] {
			return nil, apperror.InvalidField("paths", "duplicate_name", fmt.Sprintf("contains two skills named %q", p.archive.Name))
		}
		names[p.archive.Name] = true
		_, err := m.queries.GetSkillByName(ctx, skillsdb.GetSkillByNameParams{WorkspaceID: wid, Name: p.archive.Name})
		if err == nil {
			return nil, apperror.InvalidField("paths", "already_added", fmt.Sprintf("contains %s, which the workspace already has", p.archive.Name))
		} else if !database.IsNotFound(err) {
			return nil, err
		}
	}
	for _, p := range picked {
		added, err := m.add(ctx, wid, p.archive, &p.link)
		if err != nil {
			return nil, err
		}
		out.Body.Skills = append(out.Body.Skills, added.Body)
	}
	return out, nil
}

type previewInput struct {
	Body struct {
		URL string `json:"url" minLength:"1" maxLength:"2000" doc:"A GitHub link to a folder or repository with skills"`
	}
}

type previewOutput struct {
	Body struct {
		Skills []SkillChoice `json:"skills"`
	}
}

// previewImport lists the skills in a GitHub folder, so the user can pick which ones to add
func (m *Module) previewImport(ctx context.Context, in *previewInput) (*previewOutput, error) {
	wid := principal.WorkspaceID(ctx)
	err := m.allowImport(ctx, wid)
	if err != nil {
		return nil, err
	}
	choices, err := m.previewLink(ctx, strings.TrimSpace(in.Body.URL))
	if err != nil {
		return nil, err
	}

	// The workspace's own skills are marked, since a second skill with the same name can't be added
	catalog, err := m.queries.ListSkillCatalog(ctx, wid)
	if err != nil {
		return nil, err
	}
	out := &previewOutput{}
	out.Body.Skills = []SkillChoice{}
	for _, c := range choices {
		c.Exists = slices.ContainsFunc(catalog, func(r skillsdb.ListSkillCatalogRow) bool { return r.Name == c.Name })
		out.Body.Skills = append(out.Body.Skills, c)
	}
	return out, nil
}

// allowImport takes one of the caller's downloads, since a link can point at a large repository
func (m *Module) allowImport(ctx context.Context, wid string) error {
	return middleware.CheckRateLimit(ctx, m.deps.ImportLimiter, "skill-import:"+wid+":"+principal.CallerID(ctx))
}

// fetch downloads the skill a link points at, within the caller's share of downloads
func (m *Module) fetch(ctx context.Context, wid, rawURL string) (*Archive, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := m.allowImport(ctx, wid); err != nil {
		return nil, "", err
	}
	a, err := m.importFrom(ctx, rawURL)
	return a, rawURL, err
}

// add stores a new skill, with the link it came from when it was imported
func (m *Module) add(ctx context.Context, wid string, a *Archive, source *string) (*skillOutput, error) {
	// A taken name is reported before anything is stored, which the unique constraint below only backs up
	_, err := m.queries.GetSkillByName(ctx, skillsdb.GetSkillByNameParams{WorkspaceID: wid, Name: a.Name})
	if err == nil {
		return nil, apperror.AlreadyInUse("Skill name")
	} else if !database.IsNotFound(err) {
		return nil, err
	}

	// The blob is stored first, so a row never points at a blob that isn't there
	id := database.NewID()
	hash := a.Hash()
	archiveSize, err := m.saveBlob(ctx, id, hash, a)
	if err != nil {
		return nil, err
	}
	err = m.queries.CreateSkill(ctx, skillsdb.CreateSkillParams{
		ID: id, WorkspaceID: wid, Name: a.Name, Description: a.Description, ContentHash: hash, Files: manifest(a),
		FileCount: int64(len(a.Files)), Size: a.Size, ArchiveSize: archiveSize, SourceUrl: source, Now: database.Now(),
	})
	if err != nil {
		m.deleteBlobs(ctx, "skills/"+id)
		if database.IsUniqueViolation(err) {
			return nil, apperror.AlreadyInUse("Skill name")
		}
		return nil, fmt.Errorf("failed to create skill: %w", err)
	}
	return m.get(ctx, &idInput{ID: id})
}

// saveBlob stores a skill version as a normalized zip and returns its size
func (m *Module) saveBlob(ctx context.Context, skillID, hash string, a *Archive) (int64, error) {
	zipped, err := a.Zip()
	if err != nil {
		return 0, fmt.Errorf("failed to zip skill: %w", err)
	}
	err = m.deps.Storage.Save(ctx, blobKey(skillID, hash), bytes.NewReader(zipped))
	if err != nil {
		return 0, fmt.Errorf("failed to store skill: %w", err)
	}
	return int64(len(zipped)), nil
}

// deleteBlobs removes stored skill versions, logging a failure since the rows no longer refer to them
func (m *Module) deleteBlobs(ctx context.Context, prefix string) {
	if err := m.deps.Storage.DeleteAll(ctx, prefix); err != nil {
		slog.WarnContext(ctx, "Failed to delete skill files", slog.String("prefix", prefix), slog.Any("error", err))
	}
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) getSkill(ctx context.Context, wid, id string) (skillsdb.Skill, error) {
	s, err := m.queries.GetSkill(ctx, skillsdb.GetSkillParams{WorkspaceID: wid, ID: id})
	if database.IsNotFound(err) {
		return s, apperror.NotFound("Skill")
	}
	return s, err
}

func (m *Module) get(ctx context.Context, in *idInput) (*skillOutput, error) {
	wid := principal.WorkspaceID(ctx)
	s, err := m.getSkill(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	jobCount, err := m.queries.CountSkillJobs(ctx, skillsdb.CountSkillJobsParams{WorkspaceID: wid, SkillID: in.ID})
	if err != nil {
		return nil, err
	}
	return &skillOutput{Body: toDto(s, jobCount)}, nil
}

type replaceInput struct {
	ID      string `path:"id"`
	RawBody []byte `contentType:"application/zip" doc:"A zip of the new version, with the same name in its SKILL.md"`
}

// replace uploads a new version of a skill, which keeps its name since jobs and main scripts refer to its folder
func (m *Module) replace(ctx context.Context, in *replaceInput) (*skillOutput, error) {
	wid := principal.WorkspaceID(ctx)
	before, err := m.getSkill(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	a, err := ParseArchive(in.RawBody)
	if err != nil {
		return nil, err
	}

	// An uploaded version no longer matches the link the skill was imported from
	return m.update(ctx, wid, before, a, nil, "file")
}

type reimportInput struct {
	ID   string `path:"id"`
	Body struct {
		URL string `json:"url,omitempty" maxLength:"2000" doc:"The link to import the new version from, the skill's own link when omitted"`
	}
}

// reimportSkill replaces a skill with the version a link points at, by default the link it was imported from
func (m *Module) reimportSkill(ctx context.Context, in *reimportInput) (*skillOutput, error) {
	wid := principal.WorkspaceID(ctx)
	before, err := m.getSkill(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	rawURL := strings.TrimSpace(in.Body.URL)
	if rawURL == "" && before.SourceUrl != nil {
		rawURL = *before.SourceUrl
	}
	if rawURL == "" {
		return nil, apperror.InvalidField("url", "required", "is required for a skill that wasn't imported from a link")
	}
	a, source, err := m.fetch(ctx, wid, rawURL)
	if err != nil {
		return nil, err
	}
	return m.update(ctx, wid, before, a, &source, "url")
}

// update stores a new version of a skill, whose name must stay the same, and reports a problem with it on the field it came from
func (m *Module) update(ctx context.Context, wid string, before skillsdb.Skill, a *Archive, source *string, field string) (*skillOutput, error) {
	if a.Name != before.Name {
		return nil, apperror.InvalidField(field, "name_changed", fmt.Sprintf("has the name %q instead of %q; add a skill with another name as a new skill", a.Name, before.Name))
	}

	// The same content needs no new version, though the link it came from may have changed
	hash := a.Hash()
	if hash == before.ContentHash {
		if !sameSource(source, before.SourceUrl) {
			_, err := m.queries.SetSkillSource(ctx, skillsdb.SetSkillSourceParams{SourceUrl: source, UpdatedAt: database.Now(), WorkspaceID: wid, ID: before.ID})
			if err != nil {
				return nil, err
			}
		}
		return m.get(ctx, &idInput{ID: before.ID})
	}

	// The new version is stored before the row points at it, and the old one is only deleted once nothing refers to it
	archiveSize, err := m.saveBlob(ctx, before.ID, hash, a)
	if err != nil {
		return nil, err
	}
	n, err := m.queries.ReplaceSkill(ctx, skillsdb.ReplaceSkillParams{
		Description: a.Description, ContentHash: hash, Files: manifest(a), FileCount: int64(len(a.Files)), Size: a.Size, ArchiveSize: archiveSize,
		SourceUrl: source, UpdatedAt: database.Now(), WorkspaceID: wid, ID: before.ID, OldHash: before.ContentHash,
	})
	if err != nil || n == 0 {
		m.deleteBlobs(ctx, path.Dir(blobKey(before.ID, hash)))
		if err != nil {
			return nil, fmt.Errorf("failed to replace skill: %w", err)
		}
		return nil, apperror.Conflict("The skill changed while this version was added, try again")
	}
	m.deleteBlobs(ctx, path.Dir(blobKey(before.ID, before.ContentHash)))
	return m.get(ctx, &idInput{ID: before.ID})
}

func sameSource(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (m *Module) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.queries.DeleteSkill(ctx, skillsdb.DeleteSkillParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, apperror.NotFound("Skill")
	}
	m.deleteBlobs(ctx, "skills/"+in.ID)
	return nil, nil
}

type deleteManyInput struct {
	Body struct {
		IDs []string `json:"ids" minItems:"1" maxItems:"100" doc:"The skills to delete"`
	}
}

type deleteManyOutput struct {
	Body struct {
		Deleted []string `json:"deleted" doc:"The skills that were deleted"`
		Skipped []string `json:"skipped" doc:"The skills that were left alone because they don't exist, such as ones deleted meanwhile"`
	}
}

// deleteMany deletes several skills, detaching each from its jobs like a single delete does
func (m *Module) deleteMany(ctx context.Context, in *deleteManyInput) (*deleteManyOutput, error) {
	wid := principal.WorkspaceID(ctx)
	out := &deleteManyOutput{}
	out.Body.Deleted, out.Body.Skipped = []string{}, []string{}
	for _, id := range in.Body.IDs {
		n, err := m.queries.DeleteSkill(ctx, skillsdb.DeleteSkillParams{WorkspaceID: wid, ID: id})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			out.Body.Skipped = append(out.Body.Skipped, id)
			continue
		}
		m.deleteBlobs(ctx, "skills/"+id)
		out.Body.Deleted = append(out.Body.Deleted, id)
	}
	return out, nil
}

// load reads the stored version of a skill
func (m *Module) load(ctx context.Context, s skillsdb.Skill) (*Archive, error) {
	raw, err := storage.ReadAll(ctx, m.deps.Storage, blobKey(s.ID, s.ContentHash))
	if err != nil {
		return nil, fmt.Errorf("failed to read skill %s: %w", s.Name, err)
	}
	a, err := ParseArchive(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack skill %s: %w", s.Name, err)
	}
	return a, nil
}

type getFileInput struct {
	ID   string `path:"id"`
	Path string `query:"path" required:"true"`
}

// getFile serves one file of a skill, always as a download so an HTML file in a skill never renders on the app's origin
func (m *Module) getFile(ctx context.Context, in *getFileInput) (*huma.StreamResponse, error) {
	s, err := m.getSkill(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	a, err := m.load(ctx, s)
	if err != nil {
		return nil, err
	}
	for _, f := range a.Files {
		if f.Path == in.Path {
			return attachment(path.Base(f.Path), mime.TypeByExtension(path.Ext(f.Path)), f.Content), nil
		}
	}
	return nil, apperror.NotFound("Skill file")
}

// download serves the stored zip of a skill
func (m *Module) download(ctx context.Context, in *idInput) (*huma.StreamResponse, error) {
	s, err := m.getSkill(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	raw, err := storage.ReadAll(ctx, m.deps.Storage, blobKey(s.ID, s.ContentHash))
	if errors.Is(err, storage.ErrNotExist) {
		return nil, apperror.NotFound("Skill file")
	} else if err != nil {
		return nil, err
	}
	return attachment(s.Name+".zip", "application/zip", raw), nil
}

func attachment(name, contentType string, content []byte) *huma.StreamResponse {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Content-Type", contentType)
		hctx.SetHeader("Content-Length", strconv.Itoa(len(content)))
		hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		_, _ = hctx.BodyWriter().Write(content)
	}}
}

// JobSkill attaches a skill to a job
type JobSkill struct {
	SkillID   string `json:"skillId"`
	SkillName string `json:"skillName,omitempty" readOnly:"true"`
}

type jobSkillsOutput struct {
	Body []JobSkill
}

func (m *Module) getJobSkills(ctx context.Context, in *idInput) (*jobSkillsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	rows, err := m.queries.ListJobSkills(ctx, skillsdb.ListJobSkillsParams{WorkspaceID: wid, JobID: in.ID})
	if err != nil {
		return nil, err
	}
	out := &jobSkillsOutput{Body: []JobSkill{}}
	for _, r := range rows {
		out.Body = append(out.Body, JobSkill{SkillID: r.ID, SkillName: r.Name})
	}
	return out, nil
}

type setJobSkillsInput struct {
	ID   string `path:"id"`
	Body []JobSkill
}

func (m *Module) setJobSkills(ctx context.Context, in *setJobSkillsInput) (*jobSkillsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := skillsdb.New(tx)
		err := q.ClearJobSkills(ctx, in.ID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		var total int64
		for _, js := range in.Body {
			if seen[js.SkillID] {
				continue
			}
			seen[js.SkillID] = true

			// Skills from other workspaces must never be attachable
			s, err := q.GetSkill(ctx, skillsdb.GetSkillParams{WorkspaceID: wid, ID: js.SkillID})
			if database.IsNotFound(err) {
				return apperror.NotFound("Skill")
			} else if err != nil {
				return err
			}

			// Every run copies all of the job's skills into its sandbox, which bounds how many fit
			total += s.Size
			if len(seen) > runner.MaxJobSkills {
				return apperror.InvalidField("body", "too_many_skills", fmt.Sprintf("may attach at most %d skills to a job", runner.MaxJobSkills))
			}
			if total > runner.MaxJobSkillsBytes {
				return apperror.InvalidField("body", "too_large", fmt.Sprintf("may attach at most %d MiB of skills to a job", runner.MaxJobSkillsBytes>>20))
			}
			err = q.AddJobSkill(ctx, skillsdb.AddJobSkillParams{JobID: in.ID, SkillID: js.SkillID})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.getJobSkills(ctx, &idInput{ID: in.ID})
}
