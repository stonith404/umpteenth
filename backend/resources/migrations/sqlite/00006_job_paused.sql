-- +goose Up

-- A paused job keeps its schedule and webhook but neither starts runs until it is resumed
ALTER TABLE jobs ADD COLUMN paused BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down

ALTER TABLE jobs DROP COLUMN paused;
