-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, workspace_id, name, token_hash, created_by, created_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(token_hash), sqlc.narg(created_by), sqlc.arg(created_at), sqlc.narg(expires_at));

-- name: DeleteAPIToken :execrows
DELETE FROM api_tokens WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetAPITokenByHash :one
-- unscoped: the token hash is what identifies the workspace
SELECT * FROM api_tokens WHERE token_hash = sqlc.arg(token_hash);

-- name: TouchAPIToken :exec
-- unscoped: called right after the token was resolved by its hash
UPDATE api_tokens SET last_used_at = sqlc.arg(last_used_at) WHERE id = sqlc.arg(id);
