//go:build unit

package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/italypaleale/francis/actor"
	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

const testDockerfile = "FROM debian:trixie-slim\nRUN echo hi\n"

type harness struct {
	m       *Module
	db      *database.DB
	builder *sandboxfake.Adapter
	storage storage.FileStorage
	jobID   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{db: testutil.NewDatabaseForTest(t), builder: sandboxfake.New()}
	var err error
	h.storage, err = storage.NewFilesystemStorage(t.TempDir())
	require.NoError(t, err)

	testutil.NewActorHostForTest(t, func(t *testing.T, host *local.Host) {
		h.m, err = New(Dependencies{DB: h.db, Actors: host, Storage: h.storage, Builder: h.builder, MaintenanceDisabled: true})
		require.NoError(t, err)
	})
	h.jobID = testutil.SeedJob(t, h.db, testutil.SeedWorkspace(t, h.db), "skip")
	return h
}

// seedImage inserts an image row without submitting a build task, like one whose task was lost
func (h *harness) seedImage(t *testing.T, createdAt int64) imagesdb.Image {
	t.Helper()
	id := database.NewID()
	require.NoError(t, h.m.queries.CreateImage(context.Background(), imagesdb.CreateImageParams{
		ID: id, JobID: h.jobID, DockerfileHash: playbook.HashDockerfile(testDockerfile), Dockerfile: testDockerfile, CreatedAt: createdAt,
	}))
	img, err := h.m.queries.GetImage(context.Background(), id)
	require.NoError(t, err)
	return img
}

// seedReadyImage inserts a ready image that exists in the fake engine
func (h *harness) seedReadyImage(t *testing.T, createdAt int64) imagesdb.Image {
	t.Helper()
	img := h.seedImage(t, createdAt)
	ref := h.m.tag(img)
	_, err := h.builder.BuildImage(context.Background(), sandbox.BuildSpec{Dockerfile: testDockerfile, Tag: ref})
	require.NoError(t, err)
	require.NoError(t, h.m.queries.MarkReady(context.Background(), imagesdb.MarkReadyParams{ID: img.ID, Ref: &ref, SizeBytes: new(int64(1)), FinishedAt: new(database.Now())}))
	img, err = h.m.queries.GetImage(context.Background(), img.ID)
	require.NoError(t, err)
	return img
}

func (h *harness) status(t *testing.T, id string) string {
	t.Helper()
	img, err := h.m.queries.GetImage(context.Background(), id)
	require.NoError(t, err)
	return img.Status
}

func (h *harness) job() runner.JobConfig {
	return runner.JobConfig{ID: h.jobID, Dockerfile: testDockerfile, DockerfileHash: playbook.HashDockerfile(testDockerfile)}
}

func TestInterruptedBuildIsRetriedInsteadOfFailed(t *testing.T) {
	h := newHarness(t)

	// A shutdown cancels the task's context in the middle of the build
	ctx, cancel := context.WithCancel(context.Background())
	var halted atomic.Bool
	h.builder.OnBuild(func(sandbox.BuildSpec) error {
		if halted.Load() {
			return fmt.Errorf("engine went away: %w", actor.ErrActorHalted)
		}
		cancel()
		return context.Canceled
	})
	img := h.seedImage(t, database.Now())
	err := h.m.build(ctx, img)
	require.Error(t, err, "the taskpool must see the error to retry the build")
	assert.Equal(t, StatusBuilding, h.status(t, img.ID))

	// An actor halt is also an interruption, even when the context is still alive
	halted.Store(true)
	err = h.m.build(context.Background(), img)
	require.ErrorIs(t, err, actor.ErrActorHalted)
	assert.Equal(t, StatusBuilding, h.status(t, img.ID))
}

func TestFailedBuildSavesTheLogBeforeTheStatus(t *testing.T) {
	h := newHarness(t)
	img := h.seedImage(t, database.Now())

	// Every log save records the image's status at that moment, so the last one shows whether the log was complete before the failure became visible
	var statusAtLastSave atomic.Value
	s := &countingStorage{FileStorage: h.storage, onSave: func() {
		current, err := h.m.queries.GetImage(context.Background(), img.ID)
		if err == nil {
			statusAtLastSave.Store(current.Status)
		}
	}}
	h.m.deps.Storage = s

	h.builder.OnBuild(func(sandbox.BuildSpec) error { return errors.New("exit code 3") })
	err := h.m.build(context.Background(), img)
	require.NoError(t, err, "a real build failure is final and not retried")

	img, err = h.m.queries.GetImage(context.Background(), img.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, img.Status)
	assert.Equal(t, StatusBuilding, statusAtLastSave.Load())
	require.NotNil(t, img.LogKey)
	data, err := storage.ReadAll(context.Background(), h.storage, *img.LogKey)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Build failed: exit code 3")
}

// refusingEgress refuses URLs on one host, like the egress guard refuses private ones
type refusingEgress struct{ host string }

func (e refusingEgress) CheckURL(_ context.Context, field, rawURL string) error {
	if strings.Contains(rawURL, e.host) {
		return apperror.InvalidField(field, "forbidden", "points to a private or local network address")
	}
	return nil
}

// The builder fetches ADD sources and pulls images itself, outside the egress proxy, so a Dockerfile asking for either fails before the builder sees it
func TestBuildRefusesWhatTheBuilderWouldFetchItself(t *testing.T) {
	cases := map[string]string{
		"add":                "FROM debian:trixie-slim\nADD https://example.com/file /file\n",
		"private base":       "FROM 10.0.0.5:5000/tools:1\nRUN echo hi\n",
		"private later base": "FROM debian:trixie-slim AS build\nFROM 10.0.0.5:5000/tools:1\n",
		"private copy":       "FROM debian:trixie-slim\nCOPY --from=10.0.0.5:5000/tools:1 /bin/tool /bin/tool\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.m.deps.Egress = refusingEgress{host: "10.0.0.5"}
			var built atomic.Bool
			h.builder.OnBuild(func(sandbox.BuildSpec) error {
				built.Store(true)
				return nil
			})
			id := database.NewID()
			require.NoError(t, h.m.queries.CreateImage(context.Background(), imagesdb.CreateImageParams{ID: id, JobID: h.jobID, DockerfileHash: playbook.HashDockerfile(text), Dockerfile: text, CreatedAt: database.Now()}))
			img, err := h.m.queries.GetImage(context.Background(), id)
			require.NoError(t, err)

			require.NoError(t, h.m.build(context.Background(), img))
			assert.Equal(t, StatusFailed, h.status(t, id))
			assert.False(t, built.Load())
		})
	}
}

func TestResolveImageUsesAnOlderReadyImageDuringARebuild(t *testing.T) {
	h := newHarness(t)
	ready := h.seedReadyImage(t, database.Now()-1000)
	// The rebuild's row is newer but has no task, so waiting for it would block the run
	h.seedImage(t, database.Now())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ref, id, err := h.m.ResolveImage(ctx, h.job(), func() { t.Error("the run must not wait while a ready image of the same Dockerfile exists") })
	require.NoError(t, err)
	assert.Equal(t, ready.ID, id)
	assert.Equal(t, *ready.Ref, ref)
}

func TestResolveImageRequeuesAStaleBuild(t *testing.T) {
	h := newHarness(t)
	stale := h.seedImage(t, database.Now()-staleAfter.Milliseconds()-60_000)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, id, err := h.m.ResolveImage(ctx, h.job(), func() {})
	require.NoError(t, err)
	assert.Equal(t, stale.ID, id)
	assert.Equal(t, StatusReady, h.status(t, stale.ID))
}

func TestEnsureBuildRequeuesAStaleBuild(t *testing.T) {
	h := newHarness(t)
	fresh := h.seedImage(t, database.Now())

	// A build that is merely queued is left alone
	require.NoError(t, h.m.EnsureBuild(context.Background(), h.jobID, testDockerfile))
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, StatusQueued, h.status(t, fresh.ID))

	// Once it stopped making progress for too long, its task is submitted again
	testutil.Exec(t, h.db, "UPDATE images SET created_at = $1 WHERE id = $2", database.Now()-staleAfter.Milliseconds()-60_000, fresh.ID)
	require.NoError(t, h.m.EnsureBuild(context.Background(), h.jobID, testDockerfile))
	require.Eventually(t, func() bool { return h.status(t, fresh.ID) == StatusReady }, 20*time.Second, 100*time.Millisecond)
}

// holdBuilds keeps every build running until the test ends, so requested builds stay unfinished
func (h *harness) holdBuilds(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.builder.OnBuild(func(sandbox.BuildSpec) error {
		<-release
		return nil
	})
}

// Rebuilds and playbook saves anyone can repeat must not pile up builds in the queue every workspace shares
func TestRequestBuildReusesAndLimitsUnfinishedBuilds(t *testing.T) {
	h := newHarness(t)
	h.holdBuilds(t)
	ctx := context.Background()

	// Asking again for a Dockerfile whose build is unfinished returns that build
	first, err := h.m.requestBuild(ctx, h.jobID, testDockerfile)
	require.NoError(t, err)
	again, err := h.m.requestBuild(ctx, h.jobID, testDockerfile)
	require.NoError(t, err)
	assert.Equal(t, first, again)

	// Other Dockerfiles queue builds of their own up to the limit
	for i := range maxUnfinishedBuilds - 1 {
		_, err := h.m.requestBuild(ctx, h.jobID, fmt.Sprintf("%sRUN echo %d\n", testDockerfile, i))
		require.NoError(t, err)
	}
	_, err = h.m.requestBuild(ctx, h.jobID, testDockerfile+"RUN echo one too many\n")
	require.True(t, apperror.IsCode(err, apperror.CodeRateLimited), "got %v", err)

	// A build that lost its task holds no place, so it doesn't block new ones
	require.Eventually(t, func() bool { return h.status(t, first) == StatusBuilding }, 10*time.Second, 20*time.Millisecond)
	testutil.Exec(t, h.db, "UPDATE images SET created_at = $1, started_at = $1 WHERE id = $2", database.Now()-staleAfter.Milliseconds()-60_000, first)
	_, err = h.m.requestBuild(ctx, h.jobID, testDockerfile+"RUN echo now there is room\n")
	require.NoError(t, err)
}

func TestRequestBuildLimitHoldsForConcurrentRequests(t *testing.T) {
	h := newHarness(t)
	h.holdBuilds(t)

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			_, _ = h.m.requestBuild(context.Background(), h.jobID, fmt.Sprintf("%sRUN echo %d\n", testDockerfile, i))
		})
	}
	wg.Wait()

	// The shared-cache test database refuses a busy writer instead of letting it wait, so fewer requests may get through, but never more
	var rows int
	require.NoError(t, h.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM images WHERE job_id = $1", h.jobID).Scan(&rows))
	assert.Positive(t, rows)
	assert.LessOrEqual(t, rows, maxUnfinishedBuilds)
}

// A replica without a builder must leave build tasks to replicas with one, since running one would panic on the missing builder
func TestReplicaWithoutBuilderTakesNoBuilds(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	var m *Module
	testutil.NewActorHostForTest(t, func(t *testing.T, host *local.Host) {
		var err error
		m, err = New(Dependencies{DB: db, Actors: host, MaintenanceDisabled: true})
		require.NoError(t, err)
	})
	jobID := testutil.SeedJob(t, db, testutil.SeedWorkspace(t, db), "skip")

	id, err := m.queueBuild(context.Background(), jobID, testDockerfile)
	require.NoError(t, err)
	time.Sleep(time.Second)
	img, err := m.queries.GetImage(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusQueued, img.Status)
}

func TestEnsureLocalWaitersGetTheLeadersError(t *testing.T) {
	h := newHarness(t)
	img := h.seedReadyImage(t, database.Now())
	require.NoError(t, h.builder.RemoveImage(context.Background(), *img.Ref))

	// The leader's rebuild fails, and a waiter must not report success for an image that does not exist
	leader := &localBuild{done: make(chan struct{}), err: errors.New("rebuild failed")}
	h.m.localBuilds.Store(img.ID, leader)
	close(leader.done)
	_, _, err := h.m.ensureLocal(context.Background(), img)
	require.EqualError(t, err, "rebuild failed")

	// A leader that only stopped because its own run ended leaves the waiter to rebuild the image itself
	cancelled := &localBuild{done: make(chan struct{}), err: context.Canceled, cancelled: true}
	h.m.localBuilds.Store(img.ID, cancelled)
	go func() {
		h.m.localBuilds.Delete(img.ID)
		close(cancelled.done)
	}()
	ref, id, err := h.m.ensureLocal(context.Background(), img)
	require.NoError(t, err)
	assert.Equal(t, *img.Ref, ref)
	assert.Equal(t, img.ID, id)

	// A real leader shares its failure with the caller
	require.NoError(t, h.builder.RemoveImage(context.Background(), *img.Ref))
	h.builder.OnBuild(func(sandbox.BuildSpec) error { return errors.New("no space left") })
	_, _, err = h.m.ensureLocal(context.Background(), img)
	require.ErrorContains(t, err, "no space left")
}

// countingStorage counts saves, to see which flushes reach FileStorage
type countingStorage struct {
	storage.FileStorage
	saves  atomic.Int32
	onSave func()
}

func (s *countingStorage) Save(ctx context.Context, key string, data io.Reader) error {
	s.saves.Add(1)
	if s.onSave != nil {
		s.onSave()
	}
	return s.FileStorage.Save(ctx, key, data)
}

func TestBuildLogSkipsUnchangedFlushes(t *testing.T) {
	fs, err := storage.NewFilesystemStorage(t.TempDir())
	require.NoError(t, err)
	s := &countingStorage{FileStorage: fs}
	l := newBuildLog(context.Background(), s, "images/x/build.log")

	_, _ = io.WriteString(l, "step 1\n")
	l.flush()
	l.flush()
	assert.EqualValues(t, 1, s.saves.Load(), "an unchanged log is not saved again")

	// Closing saves only what changed, and a second close is harmless
	_, _ = io.WriteString(l, "step 2\n")
	l.Close()
	l.Close()
	assert.EqualValues(t, 2, s.saves.Load())
	data, err := storage.ReadAll(context.Background(), fs, "images/x/build.log")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(string(data), "step 2\n"))
}

func TestPinFrom(t *testing.T) {
	const digest = "sha256:abc"
	assert.Equal(t, "FROM debian:trixie-slim@sha256:abc\nRUN echo hi\n", pinFrom(testDockerfile, digest))
	assert.Equal(t, "# syntax=docker/dockerfile:1\nFROM --platform=linux/amd64 golang:1.27@sha256:abc AS golang-build\nFROM golang-build\n",
		pinFrom("# syntax=docker/dockerfile:1\nFROM --platform=linux/amd64 golang:1.27 AS golang-build\nFROM golang-build\n", digest), "only the first FROM image is pinned")
	assert.Equal(t, "FROM debian@sha256:def", pinFrom("FROM debian@sha256:def", digest), "an image with a digest stays as it is")
	assert.Equal(t, "RUN echo hi\n", pinFrom("RUN echo hi\n", digest))
}

func TestIsStale(t *testing.T) {
	now := database.Now()
	assert.False(t, isStale(imagesdb.Image{CreatedAt: now - 60_000}, now))
	assert.True(t, isStale(imagesdb.Image{CreatedAt: now - staleAfter.Milliseconds() - 1}, now))
	// A build that started recently is making progress, however old its row is
	assert.False(t, isStale(imagesdb.Image{CreatedAt: now - 2*staleAfter.Milliseconds(), StartedAt: new(now - 60_000)}, now))
}

func TestGCCollectsSupersededRebuildsOfAKeptDockerfile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// The Dockerfile is still in the job's recent playbook versions, so its newest build must stay
	testutil.Exec(t, h.db, "INSERT INTO playbook_versions (job_id, version, content, author, dockerfile_hash, created_at) VALUES ($1, 1, '{}', 'user', $2, $3)",
		h.jobID, playbook.HashDockerfile(testDockerfile), database.Now())
	old := h.seedReadyImage(t, 1000)
	newest := h.seedReadyImage(t, 2000)
	require.NotEqual(t, *old.Ref, *newest.Ref, "every build gets its own tag")

	require.NoError(t, h.m.gc(ctx))
	_, err := h.m.queries.GetImage(ctx, old.ID)
	require.True(t, database.IsNotFound(err), "the superseded rebuild is collected")
	require.Equal(t, StatusReady, h.status(t, newest.ID))
	ok, err := h.builder.HasImage(ctx, *newest.Ref)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestGCCollectsTheImagesOfADeletedJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// The job's newest playbook version still names the Dockerfile, which would keep its newest build and failed builds while the job exists
	testutil.Exec(t, h.db, "INSERT INTO playbook_versions (job_id, version, content, author, dockerfile_hash, created_at) VALUES ($1, 1, '{}', 'user', $2, $3)",
		h.jobID, playbook.HashDockerfile(testDockerfile), database.Now())
	ready := h.seedReadyImage(t, 1000)
	failed := h.seedImage(t, 2000)
	logKey := "images/" + failed.ID + "/build.log"
	require.NoError(t, h.storage.Save(ctx, logKey, strings.NewReader("Build failed: exit code 1\n")))
	require.NoError(t, h.m.queries.MarkFailed(ctx, imagesdb.MarkFailedParams{ID: failed.ID, Error: new("exit code 1"), FinishedAt: new(database.Now())}))

	// Deleting a job only archives it, like ArchiveJob does
	testutil.Exec(t, h.db, "UPDATE jobs SET archived_at = $1, next_run_at = NULL WHERE id = $2", database.Now(), h.jobID)

	require.NoError(t, h.m.gc(ctx))
	h.m.PruneLocal(ctx)
	_, err := h.m.queries.GetImage(ctx, ready.ID)
	require.True(t, database.IsNotFound(err), "the ready build of a deleted job is collected")
	ok, err := h.builder.HasImage(ctx, *ready.Ref)
	require.NoError(t, err)
	require.False(t, ok, "the engine image of a deleted job is removed")
	_, err = h.m.queries.GetImage(ctx, failed.ID)
	require.True(t, database.IsNotFound(err), "the failed build of a deleted job is collected")
	_, err = storage.ReadAll(ctx, h.storage, logKey)
	require.Error(t, err, "the build log of a deleted job's image is removed")
}

func TestPruneLocalRemovesOnlyOrphanedImages(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// One image still has its row, another was left behind by a row GC collected on another replica
	kept := h.seedReadyImage(t, 1000)
	_, err := h.builder.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: testDockerfile, Tag: "umpteenth/job-gone:abc"})
	require.NoError(t, err)

	// An image whose build finished but isn't marked ready yet is known by its row and must survive
	building := h.seedImage(t, 2000)
	_, err = h.builder.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: testDockerfile, Tag: h.m.tag(building)})
	require.NoError(t, err)

	h.m.PruneLocal(ctx)
	refs, err := h.builder.ListImages(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{*kept.Ref, h.m.tag(building)}, refs)
}
