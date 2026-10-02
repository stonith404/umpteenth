-- name: CreateSkill :exec
INSERT INTO skills (id, workspace_id, name, description, content_hash, files, file_count, size, archive_size, source_url, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(description), sqlc.arg(content_hash), sqlc.arg(files), sqlc.arg(file_count),
  sqlc.arg(size), sqlc.arg(archive_size), sqlc.narg(source_url), sqlc.arg(now), sqlc.arg(now));

-- name: ReplaceSkill :execrows
-- The update only lands on the version the upload replaced, so two concurrent uploads can't leave a row pointing at a deleted blob
UPDATE skills SET description = sqlc.arg(description), content_hash = sqlc.arg(content_hash), files = sqlc.arg(files), file_count = sqlc.arg(file_count),
  size = sqlc.arg(size), archive_size = sqlc.arg(archive_size), source_url = sqlc.narg(source_url), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND content_hash = sqlc.arg(old_hash);

-- name: SetSkillSource :execrows
UPDATE skills SET source_url = sqlc.narg(source_url), updated_at = sqlc.arg(updated_at) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteSkill :execrows
DELETE FROM skills WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetSkill :one
SELECT * FROM skills WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetSkillByName :one
SELECT * FROM skills WHERE workspace_id = sqlc.arg(workspace_id) AND name = sqlc.arg(name);

-- name: ListSkillCatalog :many
SELECT id, name, description, size FROM skills WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY name;

-- name: ListSkillIDsOfWorkspace :many
SELECT id FROM skills WHERE workspace_id = sqlc.arg(workspace_id);

-- name: CountSkillJobs :one
SELECT COUNT(*) FROM job_skills js JOIN skills s ON s.id = js.skill_id WHERE s.workspace_id = sqlc.arg(workspace_id) AND js.skill_id = sqlc.arg(skill_id);

-- name: ListJobSkills :many
SELECT s.id, s.name, s.description, s.size
FROM job_skills js JOIN skills s ON s.id = js.skill_id
WHERE s.workspace_id = sqlc.arg(workspace_id) AND js.job_id = sqlc.arg(job_id) ORDER BY s.name;

-- name: ClearJobSkills :exec
DELETE FROM job_skills WHERE job_id = sqlc.arg(job_id);

-- name: AddJobSkill :exec
INSERT INTO job_skills (job_id, skill_id) VALUES (sqlc.arg(job_id), sqlc.arg(skill_id));
