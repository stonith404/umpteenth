-- name: UpsertUser :one
-- unscoped: users are instance-wide and keyed by the issuer and subject of the account they sign in with
-- Whether the email address is verified and whether the user is an instance admin come from the sign-in provider every time
INSERT INTO users (id, issuer, subject, email, email_verified, name, picture, is_admin, created_at, last_login_at)
VALUES (sqlc.arg(id), sqlc.arg(issuer), sqlc.arg(subject), sqlc.narg(email), sqlc.arg(email_verified), sqlc.narg(name), sqlc.narg(picture), sqlc.arg(is_admin), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (issuer, subject) DO UPDATE SET email = excluded.email, email_verified = excluded.email_verified, name = excluded.name, picture = excluded.picture, is_admin = excluded.is_admin, last_login_at = excluded.last_login_at
RETURNING *;

-- name: GetUser :one
-- unscoped: users are instance-wide
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: SetUserDisabled :execrows
-- unscoped: users are instance-wide, and only instance admins deactivate them
UPDATE users SET disabled_at = sqlc.narg(disabled_at) WHERE id = sqlc.arg(id);

-- name: ClaimGitHubName :one
-- unscoped: an instance-wide key-value row whose key names a GitHub username or organization
-- The first claim stores its ID and every later one leaves it, so the query returns the ID that claimed the name first
INSERT INTO kv (key, value) VALUES (sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (key) DO UPDATE SET value = kv.value
RETURNING value;

-- name: CreateSession :exec
-- unscoped: sessions belong to users, who are instance-wide
INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: GetSessionUser :one
-- unscoped: sessions belong to users, who are instance-wide
SELECT user_id FROM sessions WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: DeleteSession :exec
-- unscoped: sessions belong to users, who are instance-wide
DELETE FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteUserSessions :exec
-- unscoped: sessions belong to users, who are instance-wide
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id);

-- name: DeleteExpiredSessions :exec
-- unscoped: sessions belong to users, who are instance-wide
DELETE FROM sessions WHERE expires_at <= sqlc.arg(now);
