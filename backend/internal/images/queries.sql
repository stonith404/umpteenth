-- name: CreateImage :exec
INSERT INTO images (id, job_id, dockerfile_hash, dockerfile, status, created_at)
VALUES (sqlc.arg(id), sqlc.arg(job_id), sqlc.arg(dockerfile_hash), sqlc.arg(dockerfile), 'queued', sqlc.arg(created_at));

-- name: GetImage :one
SELECT * FROM images WHERE id = sqlc.arg(id);

-- name: LatestImageForHash :one
SELECT * FROM images WHERE job_id = sqlc.arg(job_id) AND dockerfile_hash = sqlc.arg(dockerfile_hash) ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: LatestReadyImageForHash :one
SELECT * FROM images WHERE job_id = sqlc.arg(job_id) AND dockerfile_hash = sqlc.arg(dockerfile_hash) AND status = 'ready' ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: MarkBuilding :execrows
UPDATE images SET status = 'building', started_at = sqlc.arg(started_at), base_digest = sqlc.narg(base_digest), log_key = sqlc.narg(log_key)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'building');

-- name: MarkReady :exec
UPDATE images SET status = 'ready', ref = sqlc.arg(ref), digest = sqlc.narg(digest), size_bytes = sqlc.arg(size_bytes), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: MarkFailed :exec
UPDATE images SET status = 'failed', error = sqlc.arg(error), finished_at = sqlc.arg(finished_at) WHERE id = sqlc.arg(id);

-- name: ListImagesForGC :many
SELECT i.id, i.job_id, i.dockerfile_hash, i.ref, i.status, i.created_at FROM images i ORDER BY i.job_id, i.created_at DESC;

-- name: RecentDockerfileHashes :many
SELECT DISTINCT v.dockerfile_hash FROM playbook_versions v
WHERE v.job_id = sqlc.arg(job_id) AND v.dockerfile_hash IS NOT NULL
  AND v.version > (SELECT COALESCE(MAX(pv.version), 0) - 5 FROM playbook_versions pv WHERE pv.job_id = sqlc.arg(job_id));

-- name: DeleteImage :exec
DELETE FROM images WHERE id = sqlc.arg(id);
