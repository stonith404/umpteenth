-- +goose Up
-- Learning records the tokens it used next to its cost, so a workspace that shows usage in tokens counts it wherever cost counts it, and earlier runs keep 0
ALTER TABLE runs ADD COLUMN reflection_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN verify_tokens BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE runs DROP COLUMN verify_tokens;
ALTER TABLE runs DROP COLUMN reflection_tokens;
