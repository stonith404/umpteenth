-- name: CreateJob :exec
INSERT INTO jobs (id, workspace_id, name, instruction, spec, spec_overrides, model_id, image, network, allowed_domains, allow_private_network, run_as_root, limits, self_improve, mode_pin, concurrency, cron, timezone, enabled, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(instruction), sqlc.arg(spec), sqlc.arg(spec_overrides), sqlc.narg(model_id), sqlc.narg(image), sqlc.arg(network), sqlc.arg(allowed_domains), sqlc.arg(allow_private_network), sqlc.arg(run_as_root), sqlc.arg(limits), sqlc.arg(self_improve), sqlc.narg(mode_pin), sqlc.arg(concurrency), sqlc.narg(cron), sqlc.narg(timezone), sqlc.arg(enabled), sqlc.narg(created_by), sqlc.arg(created_at), sqlc.arg(created_at));

-- name: GetJob :one
SELECT * FROM jobs WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND archived_at IS NULL;

-- name: GetJobUnscoped :one
-- unscoped: used by the job actor and webhooks, which are keyed by the globally unique job ID and then act within the job's workspace
SELECT * FROM jobs WHERE id = sqlc.arg(id);

-- name: UpdateJob :execrows
UPDATE jobs SET name = sqlc.arg(name), instruction = sqlc.arg(instruction), spec = sqlc.arg(spec), spec_overrides = sqlc.arg(spec_overrides),
  model_id = sqlc.narg(model_id), image = sqlc.narg(image), network = sqlc.arg(network),
  allowed_domains = sqlc.arg(allowed_domains), allow_private_network = sqlc.arg(allow_private_network), run_as_root = sqlc.arg(run_as_root),
  limits = sqlc.arg(limits), self_improve = sqlc.arg(self_improve), mode_pin = sqlc.narg(mode_pin), concurrency = sqlc.arg(concurrency),
  cron = sqlc.narg(cron), timezone = sqlc.narg(timezone), enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND archived_at IS NULL;

-- name: ArchiveJob :execrows
UPDATE jobs SET archived_at = sqlc.arg(archived_at), enabled = FALSE, next_run_at = NULL
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND archived_at IS NULL;

-- name: SetNextRunAt :exec
UPDATE jobs SET next_run_at = sqlc.narg(next_run_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: NextRunNumber :one
UPDATE jobs SET run_counter = run_counter + 1 WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) RETURNING run_counter;

-- name: SetWebhookTokenHash :exec
UPDATE jobs SET webhook_token_hash = sqlc.narg(webhook_token_hash), updated_at = sqlc.arg(updated_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetPlaybookVersion :exec
UPDATE jobs SET playbook_version = sqlc.arg(playbook_version), updated_at = sqlc.arg(updated_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetState :one
SELECT value FROM job_state WHERE job_id = sqlc.arg(job_id) AND key = sqlc.arg(key);

-- name: GetStateEntry :one
SELECT value, updated_at FROM job_state WHERE job_id = sqlc.arg(job_id) AND key = sqlc.arg(key);

-- name: CountState :one
SELECT COUNT(*) FROM job_state WHERE job_id = sqlc.arg(job_id);

-- name: SetState :exec
INSERT INTO job_state (job_id, key, value, updated_at) VALUES (sqlc.arg(job_id), sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT (job_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: ListState :many
SELECT key, value, updated_at FROM job_state WHERE job_id = sqlc.arg(job_id) ORDER BY key;

-- name: DeleteState :execrows
DELETE FROM job_state WHERE job_id = sqlc.arg(job_id) AND key = sqlc.arg(key);

-- name: ListActiveRunsForJob :many
SELECT id, status FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND status IN ('queued', 'provisioning', 'running', 'verifying');

-- name: GetLastRun :one
SELECT id, number, status, queued_at FROM runs
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id)
ORDER BY queued_at DESC, id DESC LIMIT 1;

-- name: RecentScriptedRuns :many
-- Two fallbacks in a row since the job graduated demote it to Assisted until it graduates again
SELECT fell_back FROM runs
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND mode = 'scripted'
  AND playbook_version >= sqlc.arg(since_version) AND status IN ('succeeded', 'failed', 'timed_out')
ORDER BY number DESC LIMIT 2;
