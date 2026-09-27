-- name: CreateSecret :exec
INSERT INTO secrets (id, workspace_id, name, value_enc, key_id, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(value_enc), sqlc.arg(key_id), sqlc.arg(now), sqlc.arg(now));

-- name: UpdateSecretValue :execrows
UPDATE secrets SET value_enc = sqlc.arg(value_enc), key_id = sqlc.arg(key_id), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetSecretByName :one
SELECT * FROM secrets WHERE workspace_id = sqlc.arg(workspace_id) AND name = sqlc.arg(name);

-- name: SecretExists :one
SELECT COUNT(*) FROM secrets WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListJobSecrets :many
SELECT js.env_name, s.id, s.name, s.value_enc FROM job_secrets js JOIN secrets s ON s.id = js.secret_id
WHERE s.workspace_id = sqlc.arg(workspace_id) AND js.job_id = sqlc.arg(job_id) ORDER BY js.env_name;

-- name: ClearJobSecrets :exec
DELETE FROM job_secrets WHERE job_id = sqlc.arg(job_id);

-- name: AddJobSecret :exec
INSERT INTO job_secrets (job_id, secret_id, env_name) VALUES (sqlc.arg(job_id), sqlc.arg(secret_id), sqlc.arg(env_name));
