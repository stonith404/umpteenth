-- +goose Up
-- The run mode pin gives way to a switch that only decides whether the job may graduate to a main script
-- An Assisted pin kept the agent on every run, which is what turning graduation off does, while the other pins go back to the automatic modes
ALTER TABLE jobs ADD COLUMN graduate BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE jobs SET graduate = FALSE WHERE mode_pin = 'assisted';
ALTER TABLE jobs DROP COLUMN mode_pin;

-- +goose Down
ALTER TABLE jobs ADD COLUMN mode_pin TEXT;
UPDATE jobs SET mode_pin = 'assisted' WHERE graduate = FALSE;
ALTER TABLE jobs DROP COLUMN graduate;
