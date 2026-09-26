-- name: CreateRun :exec
INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, triggered_by, input, instructions, playbook_version, model_id, queued_at, error, finished_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(job_id), sqlc.arg(number), sqlc.arg(status), sqlc.arg(mode), sqlc.arg(trigger), sqlc.narg(triggered_by), sqlc.narg(input), sqlc.narg(instructions), sqlc.arg(playbook_version), sqlc.narg(model_id), sqlc.arg(queued_at), sqlc.narg(error), sqlc.narg(finished_at));

-- name: GetRun :one
SELECT * FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetRunUnscoped :one
-- unscoped: used by the runs taskpool, which is keyed by the globally unique run ID
SELECT * FROM runs WHERE id = sqlc.arg(id);

-- name: ClaimRun :execrows
-- unscoped: the run ID comes from the taskpool, and only a queued run can be claimed
UPDATE runs SET status = 'provisioning', host_id = sqlc.arg(host_id), started_at = sqlc.arg(started_at), heartbeat_at = sqlc.arg(started_at), ms_queue = sqlc.arg(started_at) - queued_at
WHERE id = sqlc.arg(id) AND status = 'queued';

-- name: SetRunStatus :execrows
-- unscoped: only the replica executing the run changes its live status
UPDATE runs SET status = sqlc.arg(status) WHERE id = sqlc.arg(id) AND status IN ('provisioning', 'running', 'verifying');

-- name: SetRunSandbox :exec
-- unscoped: only the replica executing the run records its sandbox
UPDATE runs SET sandbox_adapter = sqlc.arg(sandbox_adapter), sandbox_id = sqlc.arg(sandbox_id), sandbox_isolation = sqlc.arg(sandbox_isolation),
  image_ref = sqlc.narg(image_ref), image_id = sqlc.narg(image_id), model_id = sqlc.narg(model_id), ms_provision = sqlc.arg(ms_provision)
WHERE id = sqlc.arg(id);

-- name: SetRunBrokerToken :exec
-- unscoped: only the replica executing the run sets its broker token
UPDATE runs SET broker_token_hash = sqlc.narg(broker_token_hash) WHERE id = sqlc.arg(id);

-- name: HeartbeatRun :one
-- unscoped: only the replica executing the run sends heartbeats
UPDATE runs SET heartbeat_at = sqlc.arg(heartbeat_at) WHERE id = sqlc.arg(id) RETURNING cancel_requested;

-- name: FinishRun :execrows
-- unscoped: only the replica executing the run, or the reconciler, finishes it
UPDATE runs SET status = sqlc.arg(status), finished_at = sqlc.arg(finished_at), ms_total = sqlc.narg(ms_total),
  ms_llm = sqlc.arg(ms_llm), ms_tools = sqlc.arg(ms_tools), turns = sqlc.arg(turns),
  tok_in = sqlc.arg(tok_in), tok_out = sqlc.arg(tok_out), tok_cache_read = sqlc.arg(tok_cache_read), tok_cache_write = sqlc.arg(tok_cache_write),
  cost = sqlc.arg(cost), summary = sqlc.narg(summary), outputs = sqlc.narg(outputs), error = sqlc.narg(error), broker_token_hash = NULL,
  reflection = sqlc.arg(reflection), reflection_requested_at = sqlc.narg(reflection_requested_at),
  fell_back = sqlc.arg(fell_back), verify_cost = sqlc.arg(verify_cost), verify_tokens = sqlc.arg(verify_tokens)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'provisioning', 'running', 'verifying');

-- name: RequestCancel :execrows
UPDATE runs SET cancel_requested = TRUE WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND status IN ('queued', 'provisioning', 'running', 'verifying');

-- name: CancelQueuedRun :execrows
UPDATE runs SET status = 'cancelled', finished_at = sqlc.arg(finished_at), error = 'Cancelled before it started'
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND status = 'queued';

-- name: ListStaleRuns :many
-- unscoped: the reconciler looks across workspaces for runs whose replica stopped sending heartbeats
SELECT id, sandbox_id FROM runs
WHERE status IN ('provisioning', 'running', 'verifying') AND heartbeat_at < sqlc.arg(cutoff);

-- name: SumCostSince :one
SELECT CAST(COALESCE(SUM(cost + reflection_cost + verify_cost), 0) AS BIGINT) FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND queued_at >= sqlc.arg(since);

-- name: ListRunEvents :many
SELECT seq, ts, type, span_id, ms, payload FROM run_events
WHERE run_id = sqlc.arg(run_id) AND seq > sqlc.arg(after_seq) ORDER BY seq LIMIT sqlc.arg(max_rows);

-- name: GetRunByBrokerToken :one
-- unscoped: the broker token identifies the run and through it the workspace
SELECT id FROM runs WHERE broker_token_hash = sqlc.arg(broker_token_hash) AND status IN ('provisioning', 'running', 'verifying');

-- name: ListWorkspaceIDs :many
-- unscoped: retention visits every workspace with its own retention setting
SELECT id FROM workspaces;

-- name: GetPruneWatermark :one
-- unscoped: an instance-wide key-value row whose key names the workspace
SELECT value FROM kv WHERE key = sqlc.arg(key);

-- name: SetPruneWatermark :exec
-- unscoped: an instance-wide key-value row whose key names the workspace
INSERT INTO kv (key, value) VALUES (sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (key) DO UPDATE SET value = excluded.value;

-- name: ListFinishedRunIDsBefore :many
SELECT id FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND finished_at < sqlc.arg(before) AND finished_at >= sqlc.arg(after);

-- name: SetRunFellBack :exec
-- unscoped: only the replica executing the run records that its script fell back to the agent
UPDATE runs SET fell_back = TRUE WHERE id = sqlc.arg(id);

-- name: CountActiveRuns :one
SELECT COUNT(*) FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND status IN ('queued', 'provisioning', 'running', 'verifying');

-- name: ListRunIDs :many
SELECT id FROM runs WHERE workspace_id = sqlc.arg(workspace_id);

-- name: DeleteRunEventsOf :exec
DELETE FROM run_events WHERE run_id IN (SELECT id FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id));

-- name: DeletePruneWatermark :exec
-- unscoped: an instance-wide key-value row whose key names the workspace
DELETE FROM kv WHERE key = sqlc.arg(key);

-- name: DeleteFinishedRun :execrows
-- The status guard keeps a run that was claimed or started reflecting since it was loaded from being deleted underneath its runner
DELETE FROM runs WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id)
  AND status IN ('succeeded', 'failed', 'cancelled', 'timed_out', 'skipped') AND reflection <> 'pending';
