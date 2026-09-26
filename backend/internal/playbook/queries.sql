-- name: GetVersion :one
SELECT * FROM playbook_versions WHERE job_id = sqlc.arg(job_id) AND version = sqlc.arg(version);

-- name: LatestVersion :one
SELECT CAST(COALESCE(MAX(version), 0) AS BIGINT) FROM playbook_versions WHERE job_id = sqlc.arg(job_id);

-- name: InsertVersion :exec
INSERT INTO playbook_versions (job_id, version, content, ops, summary, dockerfile_hash, author, author_user_id, source_run_id, created_at)
VALUES (sqlc.arg(job_id), sqlc.arg(version), sqlc.arg(content), sqlc.narg(ops), sqlc.narg(summary), sqlc.narg(dockerfile_hash), sqlc.arg(author), sqlc.narg(author_user_id), sqlc.narg(source_run_id), sqlc.arg(created_at));

-- name: SetJobPlaybookVersion :execrows
UPDATE jobs SET playbook_version = sqlc.arg(version), updated_at = sqlc.arg(updated_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(job_id);

-- name: AddStats :exec
-- Counters only ever grow, so concurrent runs of one job add up instead of overwriting each other
INSERT INTO playbook_stats (job_id, kind, name, hits, calls, failures, updated_at)
VALUES (sqlc.arg(job_id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(hits), sqlc.arg(calls), sqlc.arg(failures), sqlc.arg(updated_at))
ON CONFLICT (job_id, kind, name) DO UPDATE SET
  hits = playbook_stats.hits + excluded.hits,
  calls = playbook_stats.calls + excluded.calls,
  failures = playbook_stats.failures + excluded.failures,
  updated_at = excluded.updated_at;

-- name: ListStats :many
SELECT kind, name, hits, calls, failures FROM playbook_stats WHERE job_id = sqlc.arg(job_id);

-- name: SetGraduatedVersion :exec
UPDATE jobs SET graduated_version = sqlc.arg(version) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(job_id);
