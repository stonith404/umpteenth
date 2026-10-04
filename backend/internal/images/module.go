// Package images builds job images from playbook Dockerfiles, so tools are installed once instead of on every run
package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/builtin/cronjob"
	"github.com/italypaleale/francis/builtin/taskpool"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
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
	// staleAfter is how long a queued or building image may go without progress before its build task is presumed lost
	staleAfter = buildTimeout + 10*time.Minute
	// builderCapability is what replicas that can build images advertise to the build taskpool
	builderCapability = "builder"
	// maxUnfinishedBuilds is how many builds of one job rebuilds and playbook saves may keep queued or building at once
	maxUnfinishedBuilds = 3
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
	// DefaultImage is the operator-selected image, which is trusted even when it shares the job registry host
	DefaultImage string
	Jobs         JobChecker
	Playbook     CurrentDockerfile
	// MaintenanceDisabled skips the GC cron job, like the other maintenance jobs in test mode
	MaintenanceDisabled bool
	// GrantProxy lets a build reach the internet through the egress proxy until revoke is called; builds get no network without it
	GrantProxy func(token string, network sandbox.NetworkPolicy) (revoke func())
	// Egress vets the registries a Dockerfile pulls images from, which the builder reaches directly rather than through the egress proxy
	Egress URLChecker
}

// URLChecker refuses URLs on networks Umpteenth itself may not reach
type URLChecker interface {
	CheckURL(ctx context.Context, field, rawURL string) error
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

	// Build tasks require the builder capability, so a replica without a builder, e.g. one with sandbox.adapter none in a mixed cluster, never picks one up
	opts := []taskpool.Option{
		taskpool.WithHandler(m.handleBuild),
		taskpool.WithConcurrency(1),
		taskpool.WithMaxAttempts(2),
		taskpool.WithLogger(slog.Default().With("scope", "images")),
	}
	if deps.Builder != nil {
		opts = append(opts, taskpool.WithCapability(builderCapability))
	}
	pool, err := taskpool.New("image-builds", opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create image build taskpool: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to register image build taskpool: %w", err)
	}
	m.pool = pool.Service(deps.Actors.Service())

	// GC removes images from the engine or registry before it drops their rows, which only a replica with a builder can do
	if !deps.MaintenanceDisabled && deps.Builder != nil {
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

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-job-images", http.MethodGet, "/api/jobs/{id}/images", "Images"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("rebuild-job-image", http.MethodPost, "/api/jobs/{id}/images/rebuild", "Images"), auth, m.rebuild)
	httpserver.Register(api, httpserver.Operation("get-image", http.MethodGet, "/api/jobs/{id}/images/{imageId}", "Images"), auth, m.get)
}

// EnsureBuild starts a build for a Dockerfile unless one exists or is in progress
func (m *Module) EnsureBuild(ctx context.Context, jobID, dockerfile string) error {
	if m.deps.Builder == nil {
		return nil
	}
	hash := playbook.HashDockerfile(dockerfile)
	latest, err := m.queries.LatestImageForHash(ctx, imagesdb.LatestImageForHashParams{JobID: jobID, DockerfileHash: hash})
	if database.IsNotFound(err) {
		_, err = m.requestBuild(ctx, jobID, dockerfile)
		return err
	} else if err != nil {
		return err
	}

	switch {
	case latest.Status == StatusFailed:
		// A failed build is retried with a new image, so its log and error stay visible
		_, err = m.requestBuild(ctx, jobID, dockerfile)
		return err
	case latest.Status != StatusReady && isStale(latest, database.Now()):
		// An unfinished build that stopped making progress lost its task, so it is submitted again
		return m.requeue(ctx, latest)
	default:
		return nil
	}
}

// ResolveImage implements runner.ImageResolver
// It never falls back to an image of an older Dockerfile, because toolkit scripts may rely on the new tools
func (m *Module) ResolveImage(ctx context.Context, job runner.JobConfig, onWait func()) (string, string, error) {
	if job.Dockerfile == "" {
		if err := sandbox.CheckRegistrySource(job.BaseImage, m.deps.Registry, job.ID, m.deps.DefaultImage); err != nil {
			return "", "", err
		}
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
			return "", "", errors.New(derefS(img.Error))
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
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	workspaceID, err := m.queries.GetImageWorkspace(buildCtx, img.ID)
	if err == nil {
		err = m.checkDockerfile(buildCtx, dockerfile, img.JobID)
	}
	if err == nil {
		_, err = m.buildWithProxy(buildCtx, sandbox.BuildSpec{WorkspaceID: workspaceID, Dockerfile: dockerfile, Tag: ref, Logs: io.Discard, Timeout: buildTimeout, MaxSizeBytes: maxImageBytes})
	}
	if err != nil {
		lb.err = fmt.Errorf("failed to rebuild the job image on this replica: %w", err)
		lb.cancelled = ctx.Err() != nil
		return "", "", lb.err
	}
	return ref, img.ID, nil
}

// buildWithProxy runs a build under a proxy grant of its own, so its steps reach the internet like an internet sandbox, but not private networks
func (m *Module) buildWithProxy(ctx context.Context, spec sandbox.BuildSpec) (sandbox.Image, error) {
	if m.deps.GrantProxy != nil {
		spec.Broker.Token = crypto.RandomToken(32)
		revoke := m.deps.GrantProxy(spec.Broker.Token, sandbox.NetworkInternet)
		defer revoke()
	}
	return m.deps.Builder.BuildImage(ctx, spec)
}

func derefS(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
