-- +goose Up

-- Instance-wide tables
CREATE TABLE kv (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at BIGINT NOT NULL
);

-- Users sign in through several providers, so they are keyed by the issuer and the subject, since a subject is only unique at its issuer
-- is_admin, email_verified and picture come from the sign-in provider and are refreshed at every sign-in
-- Passkey accounts have the issuer passkey and their own ID as the subject, and nothing refreshes them, so instance admins set their is_admin
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT FALSE,
  name TEXT,
  picture TEXT,
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,
  disabled_at BIGINT,
  -- The next sign-in lands in the workspace the user was in last, and it has no foreign key since membership is checked whenever it is used
  last_workspace_id TEXT,
  created_at BIGINT NOT NULL,
  last_login_at BIGINT,
  UNIQUE (issuer, subject)
);

-- A browser session is on record under the hash of the random ID its signed cookie carries, so signing out or deactivating the user ends it on every replica
CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- Accounts of the passkey issuer sign in with these instead of a sign-in provider, and credential holds the WebAuthn credential record as JSON
CREATE TABLE passkeys (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  credential TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  last_used_at BIGINT
);
CREATE INDEX passkeys_user ON passkeys (user_id);

-- A passkey ceremony waits here between handing out its challenge and checking the browser's answer, which deletes it, so every challenge is answered at most once
CREATE TABLE passkey_ceremonies (
  token_hash TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  data TEXT NOT NULL,
  expires_at BIGINT NOT NULL
);
CREATE INDEX passkey_ceremonies_expires ON passkey_ceremonies (expires_at);

-- A one-time link an instance admin hands to a passkey account, which signs it in once so it can add a passkey, such as a new account or one that lost its passkeys
CREATE TABLE sign_in_links (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
);
CREATE INDEX sign_in_links_user ON sign_in_links (user_id);

CREATE TABLE workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
  created_at BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user ON workspace_members (user_id);
-- A workspace has at most one owner, which also settles two sign-ins racing to claim a workspace without one
CREATE UNIQUE INDEX workspace_members_owner ON workspace_members (workspace_id) WHERE role = 'owner';

-- An invite is either a link, whose token only the person it was sent to knows, or an email address matched at sign-in
CREATE TABLE workspace_invites (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
  email TEXT,
  token_hash TEXT UNIQUE,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  CHECK ((email IS NULL) <> (token_hash IS NULL)),
  UNIQUE (workspace_id, email)
);
CREATE INDEX workspace_invites_email ON workspace_invites (email);

-- The latest models.dev catalog, shared by every replica and kept across restarts
CREATE TABLE model_catalog (
  source TEXT PRIMARY KEY,
  data TEXT NOT NULL,
  fetched_at BIGINT NOT NULL
);

-- Tenant-owned tables (every row carries workspace_id)
-- Providers keep their model list in sync with its source: the models.dev catalog for Anthropic and OpenAI, the server's model list for other OpenAI-compatible APIs
CREATE TABLE providers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  base_url TEXT,
  api_key_enc BYTEA,
  key_id TEXT,
  synced_at BIGINT,
  sync_error TEXT,
  created_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);

-- An admin turns models off instead of deleting them, since the next sync would add a deleted model back
-- follow_catalog models take their prices and capabilities from every catalog refresh until an admin edits them
-- unlisted_at marks a synced model its source stopped listing, which is kept so its settings survive the model coming back
CREATE TABLE models (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  label TEXT,
  price_in BIGINT NOT NULL DEFAULT 0,
  price_out BIGINT NOT NULL DEFAULT 0,
  price_cache_read BIGINT NOT NULL DEFAULT 0,
  price_cache_write BIGINT NOT NULL DEFAULT 0,
  context_window BIGINT NOT NULL DEFAULT 0,
  caps TEXT NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  synced BOOLEAN NOT NULL DEFAULT FALSE,
  follow_catalog BOOLEAN NOT NULL DEFAULT FALSE,
  unlisted_at BIGINT,
  created_at BIGINT NOT NULL,
  UNIQUE (provider_id, model)
);
CREATE INDEX models_ws ON models (workspace_id);

CREATE TABLE settings (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (workspace_id, key)
);

CREATE TABLE secrets (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value_enc BYTEA NOT NULL,
  key_id TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);

-- HTTP MCP servers can log in with OAuth: optional client settings, whether the server advertises OAuth, and the encrypted login
-- A started login waits in oauth_pending until the browser comes back, so any replica can finish it
CREATE TABLE mcp_servers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT,
  transport TEXT NOT NULL,
  command TEXT,
  args TEXT NOT NULL DEFAULT '[]',
  env TEXT NOT NULL DEFAULT '{}',
  url TEXT,
  headers TEXT NOT NULL DEFAULT '{}',
  tools_cache TEXT,
  tools_cached_at BIGINT,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  oauth_config TEXT NOT NULL DEFAULT '{}',
  oauth_supported BOOLEAN,
  oauth_credentials BYTEA,
  oauth_key_id TEXT,
  oauth_expires_at BIGINT,
  oauth_refreshable BOOLEAN NOT NULL DEFAULT FALSE,
  oauth_logged_in_at BIGINT,
  oauth_pending BYTEA,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  instruction TEXT NOT NULL,
  spec TEXT NOT NULL DEFAULT '{}',
  model_id TEXT REFERENCES models(id) ON DELETE SET NULL,
  image TEXT,
  network TEXT NOT NULL DEFAULT 'internet',
  -- An allow-list job reaches only these domains, and a job may opt in to reaching private ranges such as the LAN
  allowed_domains TEXT NOT NULL DEFAULT '[]',
  allow_private_network BOOLEAN NOT NULL DEFAULT FALSE,
  run_as_root BOOLEAN NOT NULL DEFAULT FALSE,
  limits TEXT NOT NULL DEFAULT '{}',
  self_improve BOOLEAN NOT NULL DEFAULT TRUE,
  -- Whether the job may graduate to a main script
  graduate BOOLEAN NOT NULL DEFAULT TRUE,
  -- The version a job graduated at bounds the scripted runs that count towards demotion, so fallbacks before a new main don't count against it
  graduated_version BIGINT,
  playbook_version BIGINT NOT NULL DEFAULT 0,
  concurrency TEXT NOT NULL DEFAULT 'skip',
  cron TEXT,
  timezone TEXT,
  next_run_at BIGINT,
  webhook_token_hash TEXT,
  run_counter BIGINT NOT NULL DEFAULT 0,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  archived_at BIGINT
);
CREATE INDEX jobs_ws_created ON jobs (workspace_id, created_at DESC, id);
CREATE INDEX jobs_ws_name ON jobs (workspace_id, name);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  number BIGINT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  fell_back BOOLEAN NOT NULL DEFAULT FALSE,
  trigger TEXT NOT NULL,
  triggered_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  input TEXT,
  instructions TEXT,
  playbook_version BIGINT NOT NULL,
  model_id TEXT,
  image_id TEXT,
  image_ref TEXT,
  host_id TEXT,
  heartbeat_at BIGINT,
  broker_token_hash TEXT,
  sandbox_adapter TEXT,
  sandbox_id TEXT,
  sandbox_isolation TEXT,
  queued_at BIGINT NOT NULL,
  started_at BIGINT,
  finished_at BIGINT,
  ms_queue BIGINT,
  ms_provision BIGINT,
  ms_llm BIGINT NOT NULL DEFAULT 0,
  ms_tools BIGINT NOT NULL DEFAULT 0,
  ms_total BIGINT,
  turns BIGINT NOT NULL DEFAULT 0,
  tok_in BIGINT NOT NULL DEFAULT 0,
  tok_out BIGINT NOT NULL DEFAULT 0,
  tok_cache_read BIGINT NOT NULL DEFAULT 0,
  tok_cache_write BIGINT NOT NULL DEFAULT 0,
  cost BIGINT NOT NULL DEFAULT 0,
  summary TEXT,
  outputs TEXT,
  error TEXT,
  cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
  -- Reflection's outcome stays on the run, so operations it held back for review are visible even when no version was written
  reflection TEXT NOT NULL DEFAULT 'skipped',
  reflection_requested_at BIGINT,
  reflection_error TEXT,
  reflection_summary TEXT,
  reflection_ops TEXT,
  reflection_version BIGINT,
  -- Learning records the tokens it used next to its cost, so a workspace that shows usage in tokens counts it wherever cost counts it
  reflection_cost BIGINT NOT NULL DEFAULT 0,
  reflection_tokens BIGINT NOT NULL DEFAULT 0,
  verify_cost BIGINT NOT NULL DEFAULT 0,
  verify_tokens BIGINT NOT NULL DEFAULT 0,
  UNIQUE (job_id, number)
);
CREATE INDEX runs_ws_queued ON runs (workspace_id, queued_at DESC, id);
CREATE INDEX runs_ws_status ON runs (workspace_id, status, queued_at DESC);
CREATE INDEX runs_job_queued ON runs (job_id, queued_at DESC);
-- The dashboard compares each job's first and latest successful runs, which this index answers without reading any other run
CREATE INDEX runs_job_status_queued ON runs (job_id, status, queued_at);
CREATE INDEX runs_live ON runs (status, heartbeat_at);
CREATE INDEX runs_broker_token ON runs (broker_token_hash);
-- The reconciler finds reflections whose task was lost without scanning every run
CREATE INDEX runs_reflection_pending ON runs (reflection, reflection_requested_at);

-- A token acts with the role of the user who created it, so a token whose creator is gone can't act at all
CREATE TABLE api_tokens (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at BIGINT NOT NULL,
  last_used_at BIGINT,
  expires_at BIGINT
);
CREATE INDEX api_tokens_ws ON api_tokens (workspace_id, created_at DESC, id);

-- Child tables, scoped through their parent
CREATE TABLE job_mcp_servers (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  mcp_server_id TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
  allowed_tools TEXT,
  PRIMARY KEY (job_id, mcp_server_id)
);

CREATE TABLE job_secrets (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  secret_id TEXT NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
  env_name TEXT NOT NULL,
  PRIMARY KEY (job_id, env_name)
);

CREATE TABLE job_state (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at BIGINT NOT NULL,
  PRIMARY KEY (job_id, key)
);

CREATE TABLE playbook_versions (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  version BIGINT NOT NULL,
  content TEXT NOT NULL,
  ops TEXT,
  summary TEXT,
  dockerfile_hash TEXT,
  author TEXT NOT NULL,
  author_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  source_run_id TEXT,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (job_id, version)
);

-- Usage counters of learnings and toolkit scripts change with every run, so they live outside the immutable playbook versions
CREATE TABLE playbook_stats (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('learning', 'script')),
  name TEXT NOT NULL,
  hits BIGINT NOT NULL DEFAULT 0,
  calls BIGINT NOT NULL DEFAULT 0,
  failures BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL,
  PRIMARY KEY (job_id, kind, name)
);

CREATE TABLE images (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  dockerfile_hash TEXT NOT NULL,
  dockerfile TEXT NOT NULL,
  base_digest TEXT,
  status TEXT NOT NULL,
  ref TEXT,
  digest TEXT,
  size_bytes BIGINT,
  error TEXT,
  log_key TEXT,
  created_at BIGINT NOT NULL,
  started_at BIGINT,
  finished_at BIGINT
);
CREATE INDEX images_job_hash ON images (job_id, dockerfile_hash, created_at DESC);
CREATE INDEX images_job_created ON images (job_id, created_at DESC, id);

CREATE TABLE run_events (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq BIGINT NOT NULL,
  ts BIGINT NOT NULL,
  type TEXT NOT NULL,
  span_id TEXT,
  ms BIGINT,
  payload TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);

-- Blob storage for file_storage.backend: database
CREATE TABLE blobs (
  key TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  size BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
