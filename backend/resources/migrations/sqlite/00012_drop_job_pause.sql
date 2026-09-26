-- +goose Up
-- Jobs can no longer be paused, so a paused job becomes an on-demand job instead of starting to run on its schedule or webhook again
UPDATE jobs SET cron = NULL, timezone = NULL, next_run_at = NULL, webhook_token_hash = NULL WHERE enabled = FALSE AND archived_at IS NULL;
ALTER TABLE jobs DROP COLUMN enabled;

-- +goose Down
ALTER TABLE jobs ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;
