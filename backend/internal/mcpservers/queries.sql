-- name: CreateServer :exec
INSERT INTO mcp_servers (id, workspace_id, name, description, transport, command, args, env, url, headers, oauth_config, enabled, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.narg(description), sqlc.arg(transport), sqlc.narg(command), sqlc.arg(args), sqlc.arg(env), sqlc.narg(url), sqlc.arg(headers),
  sqlc.arg(oauth_config), sqlc.arg(enabled), sqlc.arg(now), sqlc.arg(now));

-- name: UpdateServer :execrows
UPDATE mcp_servers SET name = sqlc.arg(name), description = sqlc.narg(description), transport = sqlc.arg(transport), command = sqlc.narg(command),
  args = sqlc.arg(args), env = sqlc.arg(env), url = sqlc.narg(url), headers = sqlc.arg(headers), oauth_config = sqlc.arg(oauth_config),
  enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteServer :execrows
DELETE FROM mcp_servers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetServer :one
SELECT * FROM mcp_servers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListServerNames :many
SELECT name FROM mcp_servers WHERE workspace_id = sqlc.arg(workspace_id) AND enabled = TRUE ORDER BY name;

-- name: SetToolsCache :exec
UPDATE mcp_servers SET tools_cache = sqlc.arg(tools_cache), tools_cached_at = sqlc.arg(tools_cached_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetOAuthSupported :exec
UPDATE mcp_servers SET oauth_supported = sqlc.narg(oauth_supported) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SaveOAuthLogin :execrows
-- A login only lands on the server while it still has the URL and OAuth client the login was started for, so a server pointed elsewhere meanwhile never gets the token
UPDATE mcp_servers SET oauth_credentials = sqlc.arg(oauth_credentials), oauth_key_id = sqlc.arg(oauth_key_id), oauth_expires_at = sqlc.narg(oauth_expires_at),
  oauth_refreshable = sqlc.arg(oauth_refreshable), oauth_logged_in_at = sqlc.arg(oauth_logged_in_at), oauth_supported = TRUE
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND transport = 'http' AND url = sqlc.arg(url) AND oauth_config = sqlc.arg(oauth_config);

-- name: SaveOAuthTokens :execrows
-- A refresh only lands on the login it refreshed, so it can't bring back a login that was logged out or replaced meanwhile
UPDATE mcp_servers SET oauth_credentials = sqlc.arg(oauth_credentials), oauth_key_id = sqlc.arg(oauth_key_id), oauth_expires_at = sqlc.narg(oauth_expires_at),
  oauth_refreshable = sqlc.arg(oauth_refreshable)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND oauth_logged_in_at = sqlc.arg(oauth_logged_in_at);

-- name: GetOAuthLogin :one
SELECT oauth_credentials, oauth_logged_in_at FROM mcp_servers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetOAuthPending :exec
UPDATE mcp_servers SET oauth_pending = sqlc.arg(oauth_pending) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetOAuthPending :one
SELECT oauth_pending FROM mcp_servers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND oauth_pending IS NOT NULL;

-- name: ClearOAuthPending :execrows
-- Clearing only the login that was read makes it single-use, so a callback URL can't be replayed even by two requests at once
UPDATE mcp_servers SET oauth_pending = NULL WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND oauth_pending = sqlc.arg(oauth_pending);

-- name: ClearOAuthLogin :execrows
-- A started login is dropped too, so it can't finish on a server that was pointed elsewhere or logged out meanwhile
UPDATE mcp_servers SET oauth_credentials = NULL, oauth_key_id = NULL, oauth_expires_at = NULL, oauth_refreshable = FALSE, oauth_logged_in_at = NULL, oauth_pending = NULL
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ClearExpiredOAuthLogin :exec
-- The login is only cleared when it is still the one whose refresh token the authorization server rejected
UPDATE mcp_servers SET oauth_credentials = NULL, oauth_key_id = NULL, oauth_expires_at = NULL, oauth_refreshable = FALSE, oauth_logged_in_at = NULL
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND oauth_logged_in_at = sqlc.arg(oauth_logged_in_at);

-- name: LeaseOAuthRefresh :execrows
-- An instance-wide key-value row whose key names the server and whose value is when the lease runs out, so one replica at a time spends the login's refresh token
-- A lease that ran out is taken over, since its replica died before releasing it
INSERT INTO kv (key, value) VALUES (sqlc.arg(key), sqlc.arg(until))
ON CONFLICT (key) DO UPDATE SET value = excluded.value WHERE CAST(kv.value AS BIGINT) < CAST(sqlc.arg(now) AS BIGINT);

-- name: ReleaseOAuthRefresh :exec
-- Only the lease that was taken is released, so a replica whose lease ran out can't release the one that took over
DELETE FROM kv WHERE key = sqlc.arg(key) AND value = sqlc.arg(until);

-- name: ListJobServers :many
SELECT s.id, s.workspace_id, s.name, s.transport, s.command, s.args, s.env, s.url, s.headers, s.enabled, s.oauth_credentials, s.oauth_logged_in_at, jm.allowed_tools
FROM job_mcp_servers jm JOIN mcp_servers s ON s.id = jm.mcp_server_id
WHERE s.workspace_id = sqlc.arg(workspace_id) AND jm.job_id = sqlc.arg(job_id) ORDER BY s.name;

-- name: ClearJobServers :exec
DELETE FROM job_mcp_servers WHERE job_id = sqlc.arg(job_id);

-- name: AddJobServer :exec
INSERT INTO job_mcp_servers (job_id, mcp_server_id, allowed_tools) VALUES (sqlc.arg(job_id), sqlc.arg(mcp_server_id), sqlc.narg(allowed_tools));
