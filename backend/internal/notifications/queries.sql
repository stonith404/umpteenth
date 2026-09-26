-- name: GetRunDetails :one
SELECT r.id, r.job_id, j.name AS job_name, r.number, r.status, r.mode, r.trigger, r.fell_back, r.error, r.summary, r.finished_at
FROM runs r JOIN jobs j ON j.id = r.job_id
WHERE r.workspace_id = sqlc.arg(workspace_id) AND r.id = sqlc.arg(id);
