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

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  oidc_subject TEXT NOT NULL UNIQUE,
  email TEXT,
  name TEXT,
  created_at BIGINT NOT NULL,
  last_login_at BIGINT
);

-- Tenant-owned tables (every row carries workspace_id)
CREATE TABLE providers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  base_url TEXT,
  api_key_enc BYTEA,
  key_id TEXT,
  created_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);

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
  spec_overrides TEXT NOT NULL DEFAULT '{}',
  model_id TEXT REFERENCES models(id) ON DELETE SET NULL,
  image TEXT,
  network TEXT NOT NULL DEFAULT 'internet',
  run_as_root BOOLEAN NOT NULL DEFAULT FALSE,
  limits TEXT NOT NULL DEFAULT '{}',
  self_improve BOOLEAN NOT NULL DEFAULT TRUE,
  mode_pin TEXT,
  playbook_version BIGINT NOT NULL DEFAULT 0,
  concurrency TEXT NOT NULL DEFAULT 'skip',
  cron TEXT,
  timezone TEXT,
  next_run_at BIGINT,
  webhook_token_hash TEXT,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
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
  reflection TEXT NOT NULL DEFAULT 'skipped',
  reflection_cost BIGINT NOT NULL DEFAULT 0,
  verify_cost BIGINT NOT NULL DEFAULT 0,
  UNIQUE (job_id, number)
);
CREATE INDEX runs_ws_queued ON runs (workspace_id, queued_at DESC, id);
CREATE INDEX runs_ws_status ON runs (workspace_id, status, queued_at DESC);
CREATE INDEX runs_job_queued ON runs (job_id, queued_at DESC);
CREATE INDEX runs_live ON runs (status, heartbeat_at);
CREATE INDEX runs_broker_token ON runs (broker_token_hash);

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
  parent_span_id TEXT,
  ms BIGINT,
  payload TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);
CREATE INDEX run_events_ts ON run_events (ts);

-- Blob storage for FILE_BACKEND=database
CREATE TABLE blobs (
  key TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  size BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE blobs;
DROP TABLE run_events;
DROP TABLE images;
DROP TABLE playbook_versions;
DROP TABLE job_state;
DROP TABLE job_secrets;
DROP TABLE job_mcp_servers;
DROP TABLE api_tokens;
DROP TABLE runs;
DROP TABLE jobs;
DROP TABLE mcp_servers;
DROP TABLE secrets;
DROP TABLE settings;
DROP TABLE models;
DROP TABLE providers;
DROP TABLE users;
DROP TABLE workspaces;
DROP TABLE kv;
