-- +goose Up

-- Reflection suggests changes only the job's owner can make, such as more memory, a token scope or a clearer instruction, which wait on the job's Overview until someone applies or dismisses them
-- key names a suggestion's subject, so a later run updates the open suggestion instead of adding the same one again
-- limits and limits_before hold a limits suggestion's new values and the job's values when it was raised, as JSON
-- run_id is the latest run whose reflection raised it, without a foreign key so run retention keeps the suggestion
CREATE TABLE job_suggestions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  kind TEXT NOT NULL,
  text TEXT NOT NULL,
  limits TEXT,
  limits_before TEXT,
  status TEXT NOT NULL DEFAULT 'open',
  run_id TEXT,
  seen BIGINT NOT NULL DEFAULT 1,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  resolved_at BIGINT
);

-- A job has one open suggestion per subject, which later runs update
CREATE UNIQUE INDEX job_suggestions_open_key ON job_suggestions (job_id, key) WHERE status = 'open';
CREATE INDEX job_suggestions_job ON job_suggestions (workspace_id, job_id, status);

-- +goose Down

DROP TABLE job_suggestions;
