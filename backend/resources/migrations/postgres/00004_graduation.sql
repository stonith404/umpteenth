-- +goose Up
-- The version a job graduated at bounds the scripted runs that count towards demotion, so fallbacks before a new main don't count against it
ALTER TABLE jobs ADD COLUMN graduated_version BIGINT;

-- +goose Down
ALTER TABLE jobs DROP COLUMN graduated_version;
