-- +goose Up

-- When the job's instruction last changed, until a reflection saw the new one
-- A main script written for the old instruction keeps doing what that one said, so a successful scripted run is reflected on while this is set
ALTER TABLE jobs ADD COLUMN instruction_changed_at BIGINT;

-- +goose Down

ALTER TABLE jobs DROP COLUMN instruction_changed_at;
