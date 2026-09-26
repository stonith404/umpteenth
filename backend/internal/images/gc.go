package images

import (
	"context"
	"log/slog"

	"github.com/distribution/reference"

	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
)

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
	// Both sides are compared by their engine-neutral name, since engines list a tag in their own form
	known := make(map[string]bool, len(rows))
	for _, img := range rows {
		known[localName(m.tag(imagesdb.Image{ID: img.ID, JobID: img.JobID, DockerfileHash: img.DockerfileHash}))] = true
		if img.Ref != nil {
			known[localName(*img.Ref)] = true
		}
	}
	for _, ref := range local {
		if known[localName(ref)] {
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

// localName reduces a tag to the short form Docker lists, which leaves out docker.io/ for a Docker Hub image
func localName(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.FamiliarString(named)
}

// gc removes images no longer referenced by the last playbook versions of their job
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
				slog.WarnContext(ctx, "Failed to load the Dockerfiles a job still uses, keeping its images", slog.String("job", img.JobID), slog.Any("error", err))
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
		err = m.deps.Storage.DeleteAll(ctx, "images/"+img.ID)
		if err != nil {
			slog.WarnContext(ctx, "Failed to remove a job image's build log", slog.String("image", img.ID), slog.Any("error", err))
		}
		err = m.queries.DeleteImage(ctx, img.ID)
		if err != nil {
			slog.WarnContext(ctx, "Failed to remove a job image", slog.String("image", img.ID), slog.Any("error", err))
		}
	}
	return nil
}
