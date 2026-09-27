-- name: GetRun :one
SELECT id, workspace_id, job_id, number, status, mode, input, playbook_version,
  summary, outputs, error, turns, cost, ms_total, reflection, finished_at, reflection_requested_at
FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: MarkPending :execrows
-- A run can only be reflected on once it has ended, and never twice at the same time
UPDATE runs SET reflection = 'pending', reflection_error = NULL, reflection_requested_at = sqlc.arg(requested_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND reflection <> 'pending'
  AND status IN ('succeeded', 'failed', 'timed_out');

-- name: ClaimReflection :execrows
-- The task that starts a reflection moves its request time to now, so the reconciler counts from the start and not from the wait in the queue
-- The new time identifies the claim, so a task that claims the run after it, e.g. a retry on another replica, takes over and only the latest claim records a result
UPDATE runs SET reflection_requested_at = sqlc.arg(claimed_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND reflection = 'pending'
  AND reflection_requested_at = sqlc.arg(requested_at);

-- name: FinishReflection :execrows
-- Only the reflection that is still pending records its result, so one the reconciler gave up on can't overwrite a newer one or clear its pending state
UPDATE runs SET reflection = sqlc.arg(reflection), reflection_error = sqlc.narg(reflection_error), reflection_summary = sqlc.narg(reflection_summary),
  reflection_ops = sqlc.narg(reflection_ops), reflection_version = sqlc.narg(reflection_version)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND reflection = 'pending'
  AND (CAST(sqlc.narg(requested_at) AS BIGINT) IS NULL OR reflection_requested_at = CAST(sqlc.narg(requested_at) AS BIGINT));

-- name: AddReflectionSpend :exec
-- A reflection's cost and tokens count whether or not its result was recorded, since they were spent
UPDATE runs SET reflection_cost = reflection_cost + sqlc.arg(cost), reflection_tokens = reflection_tokens + sqlc.arg(tokens)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: RecentRuns :many
-- The runs before this one show whether the job got faster and cheaper, and whether installs keep repeating
SELECT id, number, status, mode, turns, cost, ms_total FROM runs
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND number < sqlc.arg(number)
  AND status IN ('succeeded', 'failed', 'timed_out')
ORDER BY number DESC LIMIT sqlc.arg(max_runs);

-- name: RunEvents :many
-- The run was loaded through its workspace first, and events are reached through it
SELECT type, ms, payload FROM run_events WHERE run_id = sqlc.arg(run_id) ORDER BY seq;

-- name: FailStalePending :execrows
-- unscoped: the reconciler ends reflections across workspaces whose task was lost, so they can be started again
UPDATE runs SET reflection = 'failed', reflection_error = 'Reflection did not finish, start it again from the run page'
WHERE reflection = 'pending' AND reflection_requested_at < sqlc.arg(cutoff);

-- name: SumSpentSince :one
SELECT CAST(COALESCE(SUM(cost + reflection_cost + verify_cost), 0) AS BIGINT) FROM runs
WHERE workspace_id = sqlc.arg(workspace_id) AND queued_at >= sqlc.arg(since);
