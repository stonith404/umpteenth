-- +goose Up
-- Reflection's outcome stays on the run, so operations it held back for review are visible even when no version was written
ALTER TABLE runs ADD COLUMN reflection_error TEXT;
ALTER TABLE runs ADD COLUMN reflection_summary TEXT;
ALTER TABLE runs ADD COLUMN reflection_ops TEXT;
ALTER TABLE runs ADD COLUMN reflection_version BIGINT;
ALTER TABLE runs ADD COLUMN reflection_requested_at BIGINT;

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

-- The reconciler finds reflections whose task was lost without scanning every run
CREATE INDEX runs_reflection_pending ON runs (reflection, reflection_requested_at);

-- +goose Down
DROP INDEX runs_reflection_pending;
DROP TABLE playbook_stats;
ALTER TABLE runs DROP COLUMN reflection_requested_at;
ALTER TABLE runs DROP COLUMN reflection_version;
ALTER TABLE runs DROP COLUMN reflection_ops;
ALTER TABLE runs DROP COLUMN reflection_summary;
ALTER TABLE runs DROP COLUMN reflection_error;
