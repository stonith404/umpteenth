-- name: ListSettings :many
SELECT key, value FROM settings WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertSetting :exec
INSERT INTO settings (workspace_id, key, value) VALUES (sqlc.arg(workspace_id), sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (workspace_id, key) DO UPDATE SET value = excluded.value;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE workspace_id = sqlc.arg(workspace_id) AND key = sqlc.arg(key);
