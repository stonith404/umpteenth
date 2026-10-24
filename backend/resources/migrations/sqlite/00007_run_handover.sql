-- +goose Up

-- A scripted run whose main script handed a case to the agent with ump handover, which unlike a fallback doesn't mean main is broken
ALTER TABLE runs ADD COLUMN handed_over BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down

ALTER TABLE runs DROP COLUMN handed_over;
