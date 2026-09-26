-- name: UpsertUser :one
-- unscoped: users are instance-wide and keyed by their OIDC subject
INSERT INTO users (id, oidc_subject, email, name, created_at, last_login_at)
VALUES (sqlc.arg(id), sqlc.arg(oidc_subject), sqlc.narg(email), sqlc.narg(name), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (oidc_subject) DO UPDATE SET email = excluded.email, name = excluded.name, last_login_at = excluded.last_login_at
RETURNING *;

-- name: GetUser :one
-- unscoped: users are instance-wide
SELECT * FROM users WHERE id = sqlc.arg(id);
