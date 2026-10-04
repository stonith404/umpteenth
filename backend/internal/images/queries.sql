-- name: CreateImage :exec
INSERT INTO images (id, job_id, dockerfile_hash, dockerfile, status, created_at)
VALUES (sqlc.arg(id), sqlc.arg(job_id), sqlc.arg(dockerfile_hash), sqlc.arg(dockerfile), 'queued', sqlc.arg(created_at));

-- name: GetImage :one
SELECT * FROM images WHERE id = sqlc.arg(id);

-- name: LatestImageForHash :one
SELECT * FROM images WHERE job_id = sqlc.arg(job_id) AND dockerfile_hash = sqlc.arg(dockerfile_hash) ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: LatestReadyImageForHash :one
SELECT * FROM images WHERE job_id = sqlc.arg(job_id) AND dockerfile_hash = sqlc.arg(dockerfile_hash) AND status = 'ready' ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: LockJobBuilds :exec
-- unscoped: callers pass a job they already reached through its workspace
-- Rewriting a column with itself locks the job's row, so concurrent build requests of one job take turns and see each other's builds
UPDATE jobs SET updated_at = updated_at WHERE id = sqlc.arg(job_id);

-- name: CountUnfinishedBuilds :one
SELECT COUNT(*) FROM images
WHERE job_id = sqlc.arg(job_id) AND status IN ('queued', 'building') AND COALESCE(started_at, created_at) > sqlc.arg(since);

-- name: MarkBuilding :exec
UPDATE images SET status = 'building', started_at = sqlc.arg(started_at), base_digest = sqlc.narg(base_digest), log_key = sqlc.narg(log_key)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'building');

-- name: MarkReady :exec
UPDATE images SET status = 'ready', ref = sqlc.arg(ref), digest = sqlc.narg(digest), size_bytes = sqlc.arg(size_bytes), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: MarkFailed :exec
UPDATE images SET status = 'failed', error = sqlc.arg(error), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: ListImagesForGC :many
SELECT i.id, i.job_id, i.dockerfile_hash, i.ref, i.status FROM images i ORDER BY i.job_id, i.created_at DESC;

-- name: RecentDockerfileHashes :many
-- unscoped: GC walks the images of every workspace and reaches the job by its globally unique ID
-- A deleted job is only archived and never comes back, so none of its Dockerfiles count as in use and GC collects all of its images
SELECT DISTINCT v.dockerfile_hash FROM playbook_versions v
WHERE v.job_id = sqlc.arg(job_id) AND v.dockerfile_hash IS NOT NULL
  AND v.version > (SELECT COALESCE(MAX(pv.version), 0) - 5 FROM playbook_versions pv WHERE pv.job_id = sqlc.arg(job_id))
  AND EXISTS (SELECT 1 FROM jobs j WHERE j.id = v.job_id AND j.archived_at IS NULL);

-- name: DeleteImage :exec
DELETE FROM images WHERE id = sqlc.arg(id);

-- name: ListImageIDsOfWorkspace :many
SELECT i.id FROM images i JOIN jobs j ON j.id = i.job_id WHERE j.workspace_id = sqlc.arg(workspace_id);

-- name: GetImageWorkspace :one
-- unscoped: the durable build task identifies its image and inherits the owning job's workspace
SELECT j.workspace_id FROM images i JOIN jobs j ON j.id = i.job_id WHERE i.id = sqlc.arg(id);
