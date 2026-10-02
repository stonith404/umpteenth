-- +goose Up

-- Agent skills are uploaded as zips, kept in file storage under their content hash, and copied into a job's sandboxes
-- files is the manifest of the zip, so listing a skill never opens its blob, and source_url is the link a skill was imported from
CREATE TABLE skills (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  files TEXT NOT NULL DEFAULT '[]',
  file_count BIGINT NOT NULL,
  size BIGINT NOT NULL,
  archive_size BIGINT NOT NULL,
  source_url TEXT,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);

-- Child table, scoped through its parent
CREATE TABLE job_skills (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  PRIMARY KEY (job_id, skill_id)
);

-- +goose Down

DROP TABLE job_skills;
DROP TABLE skills;
