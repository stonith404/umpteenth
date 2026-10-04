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

-- name: GetUserByIdentity :one
-- unscoped: users are instance-wide and keyed by the issuer and subject of the account they sign in with
SELECT id FROM users WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: SetUserDisabled :execrows
-- unscoped: users are instance-wide, and only instance admins deactivate them
UPDATE users SET disabled_at = sqlc.narg(disabled_at) WHERE id = sqlc.arg(id);

-- name: ClaimGitHubName :one
-- unscoped: an instance-wide key-value row whose key names a GitHub username or organization
-- The first claim stores its ID and every later one leaves it, so the query returns the ID that claimed the name first
INSERT INTO kv (key, value) VALUES (sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (key) DO UPDATE SET value = kv.value
RETURNING value;

-- name: GetGitHubClaim :one
-- unscoped: an instance-wide key-value row whose key names a GitHub username or organization
SELECT value FROM kv WHERE key = sqlc.arg(key);

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

-- name: CreateLocalUser :one
-- unscoped: users are instance-wide, and a passkey account is its own subject at the passkey issuer
INSERT INTO users (id, issuer, subject, email, email_verified, name, is_admin, created_at)
VALUES (sqlc.arg(id), sqlc.arg(issuer), sqlc.arg(id), sqlc.narg(email), sqlc.arg(email_verified), sqlc.arg(name), sqlc.arg(is_admin), sqlc.arg(now))
RETURNING *;

-- name: UpdateLocalUser :one
-- unscoped: users are instance-wide, and only passkey accounts keep a name and email address of their own
-- A changed address loses the verification an instance admin gave it by entering it
UPDATE users SET name = sqlc.arg(name), email = sqlc.narg(email), email_verified = CASE WHEN email = sqlc.narg(email) THEN email_verified ELSE FALSE END WHERE id = sqlc.arg(id) AND issuer = sqlc.arg(issuer)
RETURNING *;

-- name: SetLocalUserAdmin :execrows
-- unscoped: users are instance-wide, and sign-in providers decide on the admins among their own accounts at every sign-in
UPDATE users SET is_admin = sqlc.arg(is_admin) WHERE id = sqlc.arg(id) AND issuer = sqlc.arg(issuer);

-- name: TouchLogin :exec
-- unscoped: users are instance-wide
UPDATE users SET last_login_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: LockSetup :exec
-- unscoped: an instance-wide key-value row that only serializes setting up the first account
-- Writing the row makes concurrent setups wait for each other on Postgres, where SQLite serializes writes anyway
INSERT INTO kv (key, value) VALUES ('passkey-setup', '') ON CONFLICT (key) DO UPDATE SET value = kv.value;

-- name: CountUsers :one
-- unscoped: users are instance-wide
SELECT COUNT(*) FROM users;

-- name: CreatePasskey :one
-- unscoped: passkeys belong to users, who are instance-wide
INSERT INTO passkeys (id, user_id, credential_id, name, credential, created_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(credential_id), sqlc.arg(name), sqlc.arg(credential), sqlc.arg(created_at))
RETURNING *;

-- name: ListUserPasskeys :many
-- unscoped: passkeys belong to users, who are instance-wide
SELECT * FROM passkeys WHERE user_id = sqlc.arg(user_id) ORDER BY created_at, id;

-- name: GetPasskeyByCredentialID :one
-- unscoped: a credential ID is unique across the instance, and the passkey names the user it signs in
SELECT * FROM passkeys WHERE credential_id = sqlc.arg(credential_id);

-- name: UsePasskey :exec
-- unscoped: passkeys belong to users, who are instance-wide
-- The credential record changes at every use, such as its signature counter and backup state
UPDATE passkeys SET credential = sqlc.arg(credential), last_used_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: RenamePasskey :execrows
-- unscoped: passkeys belong to users, who are instance-wide
UPDATE passkeys SET name = sqlc.arg(name) WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DeletePasskey :execrows
-- unscoped: passkeys belong to users, who are instance-wide
DELETE FROM passkeys WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CountUserPasskeys :one
-- unscoped: passkeys belong to users, who are instance-wide
SELECT COUNT(*) FROM passkeys WHERE user_id = sqlc.arg(user_id);

-- name: CreatePasskeyCeremony :exec
-- unscoped: a ceremony is known only by its token, which the browser that started it keeps
INSERT INTO passkey_ceremonies (token_hash, kind, data, expires_at) VALUES (sqlc.arg(token_hash), sqlc.arg(kind), sqlc.arg(data), sqlc.arg(expires_at));

-- name: TakePasskeyCeremony :one
-- unscoped: a ceremony is known only by its token, which the browser that started it keeps
-- Taking it deletes it, so a challenge can't be answered twice
DELETE FROM passkey_ceremonies WHERE token_hash = sqlc.arg(token_hash) AND kind = sqlc.arg(kind) AND expires_at > sqlc.arg(now) RETURNING data;

-- name: DeleteExpiredPasskeyCeremonies :exec
-- unscoped: ceremonies are instance-wide
DELETE FROM passkey_ceremonies WHERE expires_at <= sqlc.arg(now);

-- name: CreateSignInLink :exec
-- unscoped: sign-in links belong to users, who are instance-wide
INSERT INTO sign_in_links (token_hash, user_id, created_at, expires_at) VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: DeleteUserSignInLinks :exec
-- unscoped: sign-in links belong to users, who are instance-wide
DELETE FROM sign_in_links WHERE user_id = sqlc.arg(user_id);

-- name: TakeSignInLink :one
-- unscoped: a sign-in link is known only by its token
-- Taking it deletes it, so the link signs in once
DELETE FROM sign_in_links WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now) RETURNING user_id;

-- name: DeleteExpiredSignInLinks :exec
-- unscoped: sign-in links are instance-wide
DELETE FROM sign_in_links WHERE expires_at <= sqlc.arg(now);

-- name: RevokeUserInvites :exec
-- unscoped: deactivating an instance-wide user revokes every invitation they created
DELETE FROM workspace_invites WHERE created_by = sqlc.arg(user_id);
