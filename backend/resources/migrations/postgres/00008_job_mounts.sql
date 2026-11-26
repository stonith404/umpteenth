-- +goose Up

-- A job's mounts name host folders from the sandbox configuration, with the mode it may use them in
ALTER TABLE jobs ADD COLUMN mounts TEXT NOT NULL DEFAULT '[]';

-- +goose Down

ALTER TABLE jobs DROP COLUMN mounts;
