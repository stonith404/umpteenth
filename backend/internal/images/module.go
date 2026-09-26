// Package images builds job images from playbook Dockerfiles, so tools are installed once instead of on every run (PLAN.md §4.11)
package images

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/actor"
	"github.com/italypaleale/francis/builtin/cronjob"
	"github.com/italypaleale/francis/builtin/taskpool"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// Image statuses
const (
	StatusQueued   = "queued"
	StatusBuilding = "building"
	StatusReady    = "ready"
	StatusFailed   = "failed"
)

const (
	buildTimeout  = 15 * time.Minute
	maxImageBytes = 5 << 30
	waitPoll      = 2 * time.Second
	keepVersions  = 5
	// staleAfter is how long a queued or building image may go without progress before its build task is presumed lost
	staleAfter = buildTimeout + 10*time.Minute
)

// JobChecker verifies a job belongs to the caller's workspace, since images are scoped through their job
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

// CurrentDockerfile returns the Dockerfile of the job's current playbook
type CurrentDockerfile interface {
	CurrentDockerfile(ctx context.Context, jobID string) (string, error)
}

type Dependencies struct {
	DB      *database.DB
	Actors  francishost.Host
	Storage storage.FileStorage
	// Builder is nil when the active sandbox adapter cannot build images
	Builder  sandbox.ImageBuilder
	Registry string
	Jobs     JobChecker
	Playbook CurrentDockerfile
	// MaintenanceDisabled skips the GC cron job, like Pocket ID does in test mode
	MaintenanceDisabled bool
}

type Module struct {
	deps    Dependencies
	queries *imagesdb.Queries
	pool    *taskpool.TaskPoolService

	// localBuilds dedups builds of an image missing from this replica's cache
	localBuilds sync.Map
}

func New(deps Dependencies) (*Module, error) {
	m := &Module{deps: deps, queries: imagesdb.New(deps.DB)}

	pool, err := taskpool.New("image-builds",
		taskpool.WithHandler(m.handleBuild),
		taskpool.WithConcurrency(1),
		taskpool.WithMaxAttempts(2),
		taskpool.WithLogger(slog.Default().With("scope", "images")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create image build taskpool: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to register image build taskpool: %w", err)
	}
	m.pool = pool.Service(deps.Actors.Service())

	if !deps.MaintenanceDisabled {
		gc, err := cronjob.New("ImageGC", cronjob.WithCron("43 4 * * *"), cronjob.WithJob(m.gc), cronjob.WithJitter(10*time.Minute), cronjob.WithLogger(slog.Default()))
		if err != nil {
			return nil, fmt.Errorf("failed to create image GC: %w", err)
		}
		err = deps.Actors.RegisterBuiltInActor(gc)
		if err != nil {
			return nil, fmt.Errorf("failed to register image GC: %w", err)
		}
	}
	return m, nil
}

// SetDependencies wires the job lookups, which are built after this module
func (m *Module) SetDependencies(jobs JobChecker, pb CurrentDockerfile) {
	m.deps.Jobs = jobs
	m.deps.Playbook = pb
}

// EnsureBuild starts a build for a Dockerfile unless one exists or is in progress
func (m *Module) EnsureBuild(ctx context.Context, jobID, dockerfile string) error {
	if m.deps.Builder == nil {
		return nil
	}
	hash := playbook.HashDockerfile(dockerfile)
	latest, err := m.queries.LatestImageForHash(ctx, imagesdb.LatestImageForHashParams{JobID: jobID, DockerfileHash: hash})
	if database.IsNotFound(err) {
		_, err = m.queueBuild(ctx, jobID, dockerfile)
		return err
	} else if err != nil {
		return err
	}

	switch {
	case latest.Status == StatusFailed:
		// A failed build is retried with a new image, so its log and error stay visible
		_, err = m.queueBuild(ctx, jobID, dockerfile)
		return err
	case latest.Status != StatusReady && isStale(latest, database.Now()):
		// An unfinished build that stopped making progress lost its task, so it is submitted again
		return m.requeue(ctx, latest)
	default:
		return nil
	}
}

func (m *Module) queueBuild(ctx context.Context, jobID, dockerfile string) (string, error) {
	id := database.NewID()
	err := m.queries.CreateImage(ctx, imagesdb.CreateImageParams{ID: id, JobID: jobID, DockerfileHash: playbook.HashDockerfile(dockerfile), Dockerfile: dockerfile, CreatedAt: database.Now()})
	if err != nil {
		return "", fmt.Errorf("failed to create image: %w", err)
	}

	// A row without a build task would stay queued, so it is removed and the next caller queues a fresh build
	_, err = m.pool.Submit(ctx, buildTask{ImageID: id}, taskpool.WithTaskKey(id))
	if err != nil {
		delErr := m.queries.DeleteImage(context.WithoutCancel(ctx), id)
		if delErr != nil {
			slog.WarnContext(ctx, "Failed to remove an image whose build could not be queued", slog.String("image", id), slog.Any("error", delErr))
		}
		return "", fmt.Errorf("failed to queue image build: %w", err)
	}
	return id, nil
}

// requeue submits the build task of an unfinished image again
// The image ID is the task key, so this is a no-op while the original task is still pending or running
func (m *Module) requeue(ctx context.Context, img imagesdb.Image) error {
	slog.WarnContext(ctx, "Requeueing an image build that stopped making progress", slog.String("image", img.ID), slog.String("status", img.Status))
	_, err := m.pool.Submit(ctx, buildTask{ImageID: img.ID}, taskpool.WithTaskKey(img.ID))
	if err != nil {
		return fmt.Errorf("failed to requeue image build: %w", err)
	}
	return nil
}

// isStale reports whether a queued or building image went longer without progress than any build may take
func isStale(img imagesdb.Image, now int64) bool {
	since := img.CreatedAt
	if img.StartedAt != nil {
		since = *img.StartedAt
	}
	return now-since > staleAfter.Milliseconds()
}

type buildTask struct {
	ImageID string `json:"imageId"`
}

func (m *Module) handleBuild(ctx context.Context, task taskpool.Task) error {
	var t buildTask
	err := task.Decode(&t)
	if err != nil {
		return err
	}
	img, err := m.queries.GetImage(ctx, t.ImageID)
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if img.Status == StatusReady || img.Status == StatusFailed {
		return nil
	}

	_, err = m.build(ctx, img)
	return err
}

// build pins the base image, runs the build and records the result
func (m *Module) build(ctx context.Context, img imagesdb.Image) (imagesdb.Image, error) {
	logKey := "images/" + img.ID + "/build.log"
	logs := newBuildLog(ctx, m.deps.Storage, logKey)
	defer logs.Close()

	// Pinning FROM to a digest makes every replica build the same image
	dockerfile, baseDigest := img.Dockerfile, img.BaseDigest
	if baseDigest == nil {
		if ref := firstFrom(dockerfile); ref != "" && !strings.Contains(ref, "@sha256:") {
			fmt.Fprintf(logs, "Resolving base image %s\n", ref)
			digest, err := m.deps.Builder.ResolveDigest(ctx, ref)
			if err == nil && digest != "" {
				baseDigest = &digest
			} else if err != nil {
				fmt.Fprintf(logs, "Could not pin the base image, building from the tag: %v\n", err)
			}
		}
	}
	if baseDigest != nil {
		dockerfile = pinFrom(dockerfile, *baseDigest)
	}

	_, err := m.queries.MarkBuilding(ctx, imagesdb.MarkBuildingParams{ID: img.ID, StartedAt: new(database.Now()), BaseDigest: baseDigest, LogKey: &logKey})
	if err != nil {
		return img, err
	}

	built, err := m.deps.Builder.BuildImage(ctx, sandbox.BuildSpec{
		Dockerfile:   dockerfile,
		Tag:          m.tag(img),
		Push:         m.deps.Registry != "",
		Logs:         logs,
		Timeout:      buildTimeout,
		MaxSizeBytes: maxImageBytes,
	})

	// A build interrupted by a shutdown or an actor halt says nothing about the Dockerfile, so the taskpool retries it instead of failing the image for good
	if err != nil && (ctx.Err() != nil || errors.Is(err, actor.ErrActorHalted)) {
		fmt.Fprintf(logs, "\nBuild interrupted, it will be retried: %v\n", err)
		logs.Close()
		return img, fmt.Errorf("image build interrupted: %w", err)
	}

	// The log is saved before the terminal status, so whoever sees the result can also read the complete log
	if err != nil {
		fmt.Fprintf(logs, "\nBuild failed: %v\n", err)
		logs.Close()
		markErr := m.queries.MarkFailed(context.WithoutCancel(ctx), imagesdb.MarkFailedParams{ID: img.ID, Error: new(err.Error()), FinishedAt: new(database.Now())})
		if markErr != nil {
			slog.ErrorContext(ctx, "Failed to record a failed image build", slog.String("image", img.ID), slog.Any("error", markErr))
		}
		return img, nil
	}
	fmt.Fprintf(logs, "\nBuilt %s (%d MB)\n", built.Ref, built.SizeBytes>>20)
	logs.Close()

	// The image exists now, so its status is recorded even when the task is being stopped
	err = m.queries.MarkReady(context.WithoutCancel(ctx), imagesdb.MarkReadyParams{ID: img.ID, Ref: &built.Ref, Digest: nilIfEmpty(built.Digest), SizeBytes: &built.SizeBytes, FinishedAt: new(database.Now())})
	if err != nil {
		return img, err
	}
	return m.queries.GetImage(ctx, img.ID)
}

// tag names one build, so a rebuild of the same Dockerfile never repoints a tag that runs or other replicas already rely on
func (m *Module) tag(img imagesdb.Image) string {
	name := "umpteenth/job-" + img.JobID
	if m.deps.Registry != "" {
		name = strings.TrimRight(m.deps.Registry, "/") + "/job-" + img.JobID
	}
	id := strings.ReplaceAll(img.ID, "-", "")
	return name + ":" + img.DockerfileHash[:16] + "-" + id[len(id)-12:]
}

// ResolveImage implements runner.ImageResolver
// It never falls back to an image of an older Dockerfile, because toolkit scripts may rely on the new tools
func (m *Module) ResolveImage(ctx context.Context, job runner.JobConfig, onWait func()) (string, string, error) {
	if job.Dockerfile == "" {
		return job.BaseImage, "", nil
	}
	if m.deps.Builder == nil {
		return "", "", errors.New("the active sandbox adapter cannot build job images")
	}

	waited, requeued := false, false
	for {
		img, err := m.queries.LatestImageForHash(ctx, imagesdb.LatestImageForHashParams{JobID: job.ID, DockerfileHash: job.DockerfileHash})
		if database.IsNotFound(err) {
			_, err = m.queueBuild(ctx, job.ID, job.Dockerfile)
			if err != nil {
				return "", "", err
			}
			continue
		} else if err != nil {
			return "", "", err
		}
		if img.Status == StatusReady {
			return m.ensureLocal(ctx, img)
		}

		// An older successful build of the same Dockerfile is still valid, e.g. while a rebuild runs or after it failed
		ready, err := m.queries.LatestReadyImageForHash(ctx, imagesdb.LatestReadyImageForHashParams{JobID: job.ID, DockerfileHash: job.DockerfileHash})
		if err == nil {
			return m.ensureLocal(ctx, ready)
		} else if !database.IsNotFound(err) {
			return "", "", err
		}
		if img.Status == StatusFailed {
			return "", "", fmt.Errorf("%s", derefS(img.Error))
		}

		// A build that stopped making progress lost its task, so it is submitted again once instead of waiting forever
		if !requeued && isStale(img, database.Now()) {
			requeued = true
			err = m.requeue(ctx, img)
			if err != nil {
				return "", "", err
			}
		}

		if !waited {
			waited = true
			onWait()
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}

// CanBuild reports whether the sandbox adapter can build job images
func (m *Module) CanBuild() bool {
	return m.deps.Builder != nil
}

// Verify builds a job's Dockerfile and waits for the outcome, so reflection only applies an environment that builds
// A failed build returns the end of its log, which tells the model what went wrong
func (m *Module) Verify(ctx context.Context, jobID, dockerfile string) error {
	if m.deps.Builder == nil {
		return errors.New("the active sandbox adapter cannot build job images")
	}
	hash := playbook.HashDockerfile(dockerfile)
	queued, requeued := false, false
	for {
		img, err := m.queries.LatestImageForHash(ctx, imagesdb.LatestImageForHashParams{JobID: jobID, DockerfileHash: hash})
		switch {
		case database.IsNotFound(err) || (err == nil && img.Status == StatusFailed && !queued):
			// An earlier failure may have been transient, so this Dockerfile gets one fresh build of its own
			queued = true
			_, err = m.queueBuild(ctx, jobID, dockerfile)
			if err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		case img.Status == StatusReady:
			return nil
		case img.Status == StatusFailed:
			return fmt.Errorf("%s\n%s", derefS(img.Error), m.logTail(ctx, img, 3000))
		case !requeued && isStale(img, database.Now()):
			requeued = true
			err = m.requeue(ctx, img)
			if err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}

// logTail returns the end of an image's build log
func (m *Module) logTail(ctx context.Context, img imagesdb.Image, n int) string {
	if img.LogKey == nil {
		return ""
	}
	r, _, err := m.deps.Storage.Open(ctx, *img.LogKey)
	if err != nil {
		return ""
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	return string(raw)
}

// ensureLocal makes sure a ready image exists on this replica, rebuilding it from the pinned Dockerfile when there is no registry to pull from
func (m *Module) ensureLocal(ctx context.Context, img imagesdb.Image) (string, string, error) {
	ref := derefS(img.Ref)
	for {
		ok, err := m.deps.Builder.HasImage(ctx, ref)
		if err == nil && ok {
			return ref, img.ID, nil
		}

		// Concurrent runs on this replica share one local rebuild
		lb := &localBuild{done: make(chan struct{})}
		existing, loaded := m.localBuilds.LoadOrStore(img.ID, lb)
		if !loaded {
			return m.rebuildLocal(ctx, img, lb)
		}
		leader := existing.(*localBuild)
		select {
		case <-leader.done:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}

		// Waiters share the leader's result, unless the leader only stopped because its own run ended, which says nothing about the image
		if leader.err == nil {
			return ref, img.ID, nil
		}
		if !leader.cancelled {
			return "", "", leader.err
		}
	}
}

// localBuild is one in-flight local rebuild, whose result is shared with the runs waiting for it
// err and cancelled are written before done is closed, so waiters may read them once done is closed
type localBuild struct {
	done      chan struct{}
	err       error
	cancelled bool
}

// rebuildLocal rebuilds a ready image from its pinned Dockerfile as the leader of lb
func (m *Module) rebuildLocal(ctx context.Context, img imagesdb.Image, lb *localBuild) (string, string, error) {
	defer func() {
		m.localBuilds.Delete(img.ID)
		close(lb.done)
	}()

	// The pinned base digest makes the rebuild match the image other replicas built
	ref := derefS(img.Ref)
	dockerfile := img.Dockerfile
	if img.BaseDigest != nil {
		dockerfile = pinFrom(dockerfile, *img.BaseDigest)
	}
	_, err := m.deps.Builder.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: dockerfile, Tag: ref, Logs: io.Discard, Timeout: buildTimeout, MaxSizeBytes: maxImageBytes})
	if err != nil {
		lb.err = fmt.Errorf("failed to rebuild the job image on this replica: %w", err)
		lb.cancelled = ctx.Err() != nil
		return "", "", lb.err
	}
	return ref, img.ID, nil
}

// gc removes images no longer referenced by the last playbook versions of their job
// PruneLocal removes job images from this replica's engine that no image row refers to any more
// GC runs on one replica only, so each replica calls this for its own engine, where images of collected rows would otherwise stay forever
func (m *Module) PruneLocal(ctx context.Context) {
	if m.deps.Builder == nil {
		return
	}
	local, err := m.deps.Builder.ListImages(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to list local job images", slog.Any("error", err))
		return
	}
	rows, err := m.queries.ListImagesForGC(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to load job images", slog.Any("error", err))
		return
	}

	// Tags follow from the rows, which also covers an image that is built but not yet marked ready
	known := make(map[string]bool, len(rows))
	for _, img := range rows {
		known[m.tag(imagesdb.Image{ID: img.ID, JobID: img.JobID, DockerfileHash: img.DockerfileHash})] = true
		if img.Ref != nil {
			known[*img.Ref] = true
		}
	}
	for _, ref := range local {
		if known[ref] {
			continue
		}
		err := m.deps.Builder.RemoveImage(ctx, ref)
		if err != nil {
			slog.WarnContext(ctx, "Failed to remove an orphaned job image", slog.String("image", ref), slog.Any("error", err))
			continue
		}
		slog.InfoContext(ctx, "Removed orphaned job image", slog.String("image", ref))
	}
}

func (m *Module) gc(ctx context.Context) error {
	rows, err := m.queries.ListImagesForGC(ctx)
	if err != nil {
		return err
	}
	keep := map[string]map[string]bool{}
	newestSeen := map[string]bool{}
	for _, img := range rows {
		hashes, ok := keep[img.JobID]
		if !ok {
			list, err := m.queries.RecentDockerfileHashes(ctx, img.JobID)
			if err != nil {
				continue
			}
			hashes = map[string]bool{}
			for _, h := range list {
				if h != nil {
					hashes[*h] = true
				}
			}
			keep[img.JobID] = hashes
		}
		if img.Status == StatusBuilding || img.Status == StatusQueued {
			continue
		}

		// Of a Dockerfile still in use only the newest ready build is used, so older rebuilds of it are collected too
		// Rows come newest first, so the first ready row per Dockerfile is the one runs resolve to
		if hashes[img.DockerfileHash] {
			key := img.JobID + "/" + img.DockerfileHash
			if img.Status != StatusReady || !newestSeen[key] {
				newestSeen[key] = newestSeen[key] || img.Status == StatusReady
				continue
			}
		}

		// The row goes only once the image is gone, e.g. not while a running sandbox still uses it, so a later pass retries
		if img.Ref != nil && m.deps.Builder != nil {
			err := m.deps.Builder.RemoveImage(ctx, *img.Ref)
			if err != nil {
				slog.WarnContext(ctx, "Failed to remove a job image, keeping it for the next pass", slog.String("image", img.ID), slog.Any("error", err))
				continue
			}
		}
		_ = m.deps.Storage.DeleteAll(ctx, "images/"+img.ID)
		_ = m.queries.DeleteImage(ctx, img.ID)
	}
	return nil
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-job-images", http.MethodGet, "/api/jobs/{id}/images", "Images"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("rebuild-job-image", http.MethodPost, "/api/jobs/{id}/images/rebuild", "Images"), auth, m.rebuild)
	httpserver.Register(api, httpserver.Operation("get-image", http.MethodGet, "/api/jobs/{id}/images/{imageId}", "Images"), auth, m.get)
}

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

// buildLog buffers build output and saves it to FileStorage periodically, so any replica can show a live log
type buildLog struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	storage storage.FileStorage
	key     string
	ctx     context.Context
	stop    chan struct{}
	done    chan struct{}
	closed  sync.Once

	// writes counts Write calls and flushed is the count the last save saw, so an unchanged log is not uploaded again
	writes  uint64
	flushed uint64
}

func newBuildLog(ctx context.Context, s storage.FileStorage, key string) *buildLog {
	l := &buildLog{storage: s, key: key, ctx: context.WithoutCancel(ctx), stop: make(chan struct{}), done: make(chan struct{})}
	go l.loop()
	return l
}

func (l *buildLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes++
	// Very long logs keep their end, which is where build errors are
	if l.buf.Len() > 4<<20 {
		tail := append([]byte("[… earlier output omitted …]\n"), l.buf.Bytes()[l.buf.Len()-(2<<20):]...)
		l.buf.Reset()
		l.buf.Write(tail)
	}
	return l.buf.Write(p)
}

func (l *buildLog) loop() {
	defer close(l.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			l.flush()
			return
		case <-ticker.C:
			l.flush()
		}
	}
}

func (l *buildLog) flush() {
	// Nothing new was written since the last save, so there is nothing to upload
	l.mu.Lock()
	if l.writes == l.flushed {
		l.mu.Unlock()
		return
	}
	data := append([]byte(nil), l.buf.Bytes()...)
	writes := l.writes
	l.mu.Unlock()

	// A failed save is retried on the next tick, since the count only advances once the log is stored
	err := l.storage.Save(l.ctx, l.key, bytes.NewReader(data))
	if err != nil {
		slog.WarnContext(l.ctx, "Failed to save an image build log", slog.String("key", l.key), slog.Any("error", err))
		return
	}
	l.mu.Lock()
	l.flushed = writes
	l.mu.Unlock()
}

// Close saves the final log and stops the periodic saves; it is safe to call more than once
func (l *buildLog) Close() {
	l.closed.Do(func() {
		close(l.stop)
		<-l.done
	})
}

var fromLine = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// firstFrom returns the image of the first FROM instruction
func firstFrom(dockerfile string) string {
	match := fromLine.FindStringSubmatch(dockerfile)
	if match == nil {
		return ""
	}
	return match[1]
}

// pinFrom rewrites the first FROM to reference the resolved digest
func pinFrom(dockerfile, digest string) string {
	ref := firstFrom(dockerfile)
	if ref == "" || strings.Contains(ref, "@") {
		return dockerfile
	}
	pinned := ref + "@" + digest
	if at := strings.LastIndex(digest, "@"); at >= 0 {
		pinned = digest
	}
	var out strings.Builder
	replaced := false
	sc := bufio.NewScanner(strings.NewReader(dockerfile))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !replaced && fromLine.MatchString(line) {
			line = strings.Replace(line, ref, pinned, 1)
			replaced = true
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefS(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
