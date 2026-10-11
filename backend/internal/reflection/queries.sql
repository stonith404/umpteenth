-- name: GetRun :one
SELECT id, workspace_id, job_id, number, status, mode, input, instructions, playbook_version,
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
-- The job's finished runs before a given number, newest first, show whether it got faster and cheaper and which runs count toward graduation
SELECT id, number, status, mode, instructions, turns, cost, ms_total FROM runs
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

-- name: ListOpenSuggestions :many
SELECT s.id, s.key, s.kind, s.text, s.limits, s.limits_before, s.run_id, r.number AS run_number, s.seen, s.created_at, s.updated_at
FROM job_suggestions s LEFT JOIN runs r ON r.id = s.run_id AND r.workspace_id = s.workspace_id
WHERE s.workspace_id = sqlc.arg(workspace_id) AND s.job_id = sqlc.arg(job_id) AND s.status = 'open'
ORDER BY s.updated_at DESC, s.id DESC;

-- name: SuggestionsForReflection :many
-- Reflection sees the open suggestions it may update and the ones the owner dismissed lately, which it must not raise again
SELECT key, kind, text, status, seen FROM job_suggestions
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id)
  AND (status = 'open' OR (status = 'dismissed' AND resolved_at >= sqlc.arg(dismissed_since)))
ORDER BY created_at, id;

-- name: OpenSuggestionByKey :one
SELECT id, run_id, seen FROM job_suggestions
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND key = sqlc.arg(key) AND status = 'open';

-- name: InsertSuggestion :exec
INSERT INTO job_suggestions (id, workspace_id, job_id, key, kind, text, limits, limits_before, run_id, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(job_id), sqlc.arg(key), sqlc.arg(kind), sqlc.arg(text), sqlc.narg(limits), sqlc.narg(limits_before), sqlc.arg(run_id), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: RefreshSuggestion :exec
-- A later run that raises the same subject rewords the open suggestion instead of adding another
UPDATE job_suggestions SET kind = sqlc.arg(kind), text = sqlc.arg(text), limits = sqlc.narg(limits), limits_before = sqlc.narg(limits_before),
  run_id = sqlc.arg(run_id), seen = sqlc.arg(seen), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND status = 'open';

-- name: GetSuggestion :one
SELECT id, kind, status, limits, limits_before FROM job_suggestions
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND id = sqlc.arg(id);

-- name: ResolveSuggestion :execrows
-- Only an open suggestion is resolved, so two people deciding at once resolve it once
UPDATE job_suggestions SET status = sqlc.arg(status), resolved_at = sqlc.arg(resolved_at), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND id = sqlc.arg(id) AND status = 'open';

-- name: OverruleDismissal :exec
-- An apply that set the job's limits wins over a dismissal that raced it, so the suggestion's status matches the job and doesn't hold back later limits suggestions
UPDATE job_suggestions SET status = 'done', updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND job_id = sqlc.arg(job_id) AND id = sqlc.arg(id) AND status = 'dismissed';

-- name: ClearInstructionChanged :exec
-- A reflection that saw the job's changed instruction clears its stamp, unless the instruction changed again meanwhile
UPDATE jobs SET instruction_changed_at = NULL
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND instruction_changed_at = sqlc.arg(changed_at);
