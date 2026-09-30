package images

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/italypaleale/francis/actor"
	"github.com/italypaleale/francis/builtin/taskpool"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// queueBuild creates an image row for the Dockerfile and submits its build task
func (m *Module) queueBuild(ctx context.Context, jobID, dockerfile string) (string, error) {
	id := database.NewID()
	err := m.queries.CreateImage(ctx, imagesdb.CreateImageParams{ID: id, JobID: jobID, DockerfileHash: playbook.HashDockerfile(dockerfile), Dockerfile: dockerfile, CreatedAt: database.Now()})
	if err != nil {
		return "", fmt.Errorf("failed to create image: %w", err)
	}

	// A row without a build task would stay queued, so it is removed and the next caller queues a fresh build
	_, err = m.pool.Submit(ctx, buildTask{ImageID: id}, taskpool.WithTaskKey(id), taskpool.WithRequiredCapability(builderCapability))
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
	_, err := m.pool.Submit(ctx, buildTask{ImageID: img.ID}, taskpool.WithTaskKey(img.ID), taskpool.WithRequiredCapability(builderCapability))
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

	return m.build(ctx, img)
}

// build pins the base image, runs the build and records the result
// It only returns an error for an interrupted build, which the taskpool retries, since a failed build is recorded on the image
func (m *Module) build(ctx context.Context, img imagesdb.Image) error {
	logKey := "images/" + img.ID + "/build.log"
	logs := newBuildLog(ctx, m.deps.Storage, logKey)
	defer logs.Close()

	// Pinning FROM to a digest makes every replica build the same image
	dockerfile, baseDigest := img.Dockerfile, img.BaseDigest
	if baseDigest == nil {
		if ref := firstFrom(dockerfile); ref != "" && !strings.Contains(ref, "@") {
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

	err := m.queries.MarkBuilding(ctx, imagesdb.MarkBuildingParams{ID: img.ID, StartedAt: new(database.Now()), BaseDigest: baseDigest, LogKey: &logKey})
	if err != nil {
		return err
	}

	built, err := m.buildWithProxy(ctx, sandbox.BuildSpec{
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
		return fmt.Errorf("image build interrupted: %w", err)
	}

	// The log is saved before the terminal status, so whoever sees the result can also read the complete log
	if err != nil {
		fmt.Fprintf(logs, "\nBuild failed: %v\n", err)
		logs.Close()
		markErr := m.queries.MarkFailed(context.WithoutCancel(ctx), imagesdb.MarkFailedParams{ID: img.ID, Error: new(err.Error()), FinishedAt: new(database.Now())})
		if markErr != nil {
			slog.ErrorContext(ctx, "Failed to record a failed image build", slog.String("image", img.ID), slog.Any("error", markErr))
		}
		return nil
	}
	fmt.Fprintf(logs, "\nBuilt %s (%d MB)\n", built.Ref, built.SizeBytes>>20)
	logs.Close()

	// The image exists now, so its status is recorded even when the task is being stopped
	return m.queries.MarkReady(context.WithoutCancel(ctx), imagesdb.MarkReadyParams{ID: img.ID, Ref: &built.Ref, Digest: nilIfEmpty(built.Digest), SizeBytes: &built.SizeBytes, FinishedAt: new(database.Now())})
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

// fromLine matches a FROM instruction and captures its image
var fromLine = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// firstFrom returns the image of the first FROM instruction
func firstFrom(dockerfile string) string {
	match := fromLine.FindStringSubmatch(dockerfile)
	if match == nil {
		return ""
	}
	return match[1]
}

// pinFrom appends the resolved digest to the image of the first FROM, unless that image names a digest already
func pinFrom(dockerfile, digest string) string {
	loc := fromLine.FindStringSubmatchIndex(dockerfile)
	if loc == nil || strings.Contains(dockerfile[loc[2]:loc[3]], "@") {
		return dockerfile
	}
	return dockerfile[:loc[3]] + "@" + digest + dockerfile[loc[3]:]
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
