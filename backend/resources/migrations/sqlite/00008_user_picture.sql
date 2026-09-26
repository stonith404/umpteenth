-- +goose Up
-- The profile picture URL from the identity provider's picture claim, refreshed on every login
ALTER TABLE users ADD COLUMN picture TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN picture;
