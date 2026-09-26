-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = sqlc.arg(id);

-- name: EnsureWorkspace :exec
INSERT INTO workspaces (id, name, created_at) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;
