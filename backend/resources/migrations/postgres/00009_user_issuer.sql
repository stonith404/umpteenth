-- +goose Up
-- Users sign in through several providers, so they are keyed by the issuer and the subject, since a subject is only unique at its issuer
-- Existing users have no issuer, so they are removed and get a new account at their next sign-in
DELETE FROM users;
ALTER TABLE users RENAME COLUMN oidc_subject TO subject;
ALTER TABLE users ADD COLUMN issuer TEXT NOT NULL;
ALTER TABLE users DROP CONSTRAINT users_oidc_subject_key;
ALTER TABLE users ADD CONSTRAINT users_issuer_subject_key UNIQUE (issuer, subject);

-- +goose Down
DELETE FROM users;
ALTER TABLE users DROP CONSTRAINT users_issuer_subject_key;
ALTER TABLE users DROP COLUMN issuer;
ALTER TABLE users RENAME COLUMN subject TO oidc_subject;
ALTER TABLE users ADD CONSTRAINT users_oidc_subject_key UNIQUE (oidc_subject);
