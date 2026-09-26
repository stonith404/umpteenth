package images

import (
	"context"
	"database/sql"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// ImageDto is a job image
type ImageDto struct {
	ID             string  `json:"id"`
	DockerfileHash string  `json:"dockerfileHash"`
	Status         string  `json:"status"`
	Ref            *string `json:"ref"`
	Digest         *string `json:"digest"`
	BaseDigest     *string `json:"baseDigest"`
	SizeBytes      *int64  `json:"sizeBytes"`
	Error          *string `json:"error"`
	CreatedAt      int64   `json:"createdAt"`
	StartedAt      *int64  `json:"startedAt"`
	FinishedAt     *int64  `json:"finishedAt"`
}

var listSpec = &listquery.Spec{
	Select:        "SELECT id, dockerfile_hash, status, ref, digest, base_digest, size_bytes, error, created_at, started_at, finished_at FROM images",
	From:          "FROM images",
	Sorts:         map[string]string{"createdAt": "created_at", "status": "status", "sizeBytes": "size_bytes"},
	NullableSorts: []string{"sizeBytes"},
	DefaultSort:   "-createdAt",
	Search:        []string{"status", "ref", "dockerfile_hash"},
	TieBreaker:    "id",
}

type listInput struct {
	ID string `path:"id"`
	httpserver.ListParams
	Status string `query:"status"`
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[ImageDto], error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	q := listquery.New(listSpec).WhereEq("job_id", in.ID)
	q.WhereIn("status", listquery.SplitCSV(in.Status))
	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (ImageDto, error) {
		var d ImageDto
		err := rows.Scan(&d.ID, &d.DockerfileHash, &d.Status, &d.Ref, &d.Digest, &d.BaseDigest, &d.SizeBytes, &d.Error, &d.CreatedAt, &d.StartedAt, &d.FinishedAt)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type rebuildInput struct {
	ID string `path:"id"`
}

type imageIDOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

// rebuild builds the current Dockerfile again, re-resolving the base image to pick up updates
func (m *Module) rebuild(ctx context.Context, in *rebuildInput) (*imageIDOutput, error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	if m.deps.Builder == nil {
		return nil, apperror.Unsupported("The active sandbox adapter cannot build images")
	}
	dockerfile, err := m.deps.Playbook.CurrentDockerfile(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dockerfile) == "" {
		return nil, apperror.Unsupported("The job has no Dockerfile")
	}
	id, err := m.queueBuild(ctx, in.ID, dockerfile)
	if err != nil {
		return nil, err
	}
	out := &imageIDOutput{}
	out.Body.ID = id
	return out, nil
}

type getInput struct {
	ID      string `path:"id"`
	ImageID string `path:"imageId"`
}

type getOutput struct {
	Body struct {
		ImageDto
		Dockerfile string `json:"dockerfile"`
		Log        string `json:"log"`
	}
}

func (m *Module) get(ctx context.Context, in *getInput) (*getOutput, error) {
	err := m.deps.Jobs.JobExists(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	img, err := m.queries.GetImage(ctx, in.ImageID)
	if database.IsNotFound(err) || (err == nil && img.JobID != in.ID) {
		return nil, apperror.NotFound("Image")
	} else if err != nil {
		return nil, err
	}

	out := &getOutput{}
	out.Body.ImageDto = ImageDto{ID: img.ID, DockerfileHash: img.DockerfileHash, Status: img.Status, Ref: img.Ref, Digest: img.Digest, BaseDigest: img.BaseDigest,
		SizeBytes: img.SizeBytes, Error: img.Error, CreatedAt: img.CreatedAt, StartedAt: img.StartedAt, FinishedAt: img.FinishedAt}
	out.Body.Dockerfile = img.Dockerfile
	if img.LogKey != nil {
		data, err := storage.ReadAll(ctx, m.deps.Storage, *img.LogKey)
		if err == nil {
			out.Body.Log = string(data)
		}
	}
	return out, nil
}
