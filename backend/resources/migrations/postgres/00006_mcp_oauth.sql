-- +goose Up
-- HTTP MCP servers can log in with OAuth: optional client settings, whether the server advertises OAuth, and the encrypted login
-- A started login waits in oauth_pending until the browser comes back, so any replica can finish it
ALTER TABLE mcp_servers ADD COLUMN oauth_config TEXT NOT NULL DEFAULT '{}';
ALTER TABLE mcp_servers ADD COLUMN oauth_supported BOOLEAN;
ALTER TABLE mcp_servers ADD COLUMN oauth_credentials BYTEA;
ALTER TABLE mcp_servers ADD COLUMN oauth_key_id TEXT;
ALTER TABLE mcp_servers ADD COLUMN oauth_expires_at BIGINT;
ALTER TABLE mcp_servers ADD COLUMN oauth_refreshable BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE mcp_servers ADD COLUMN oauth_logged_in_at BIGINT;
ALTER TABLE mcp_servers ADD COLUMN oauth_pending BYTEA;

-- +goose Down
ALTER TABLE mcp_servers DROP COLUMN oauth_pending;
ALTER TABLE mcp_servers DROP COLUMN oauth_logged_in_at;
ALTER TABLE mcp_servers DROP COLUMN oauth_refreshable;
ALTER TABLE mcp_servers DROP COLUMN oauth_expires_at;
ALTER TABLE mcp_servers DROP COLUMN oauth_key_id;
ALTER TABLE mcp_servers DROP COLUMN oauth_credentials;
ALTER TABLE mcp_servers DROP COLUMN oauth_supported;
ALTER TABLE mcp_servers DROP COLUMN oauth_config;
