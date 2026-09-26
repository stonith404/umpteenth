-- +goose Up
-- Users sign in through several providers, so they are keyed by the issuer and the subject, since a subject is only unique at its issuer
-- SQLite can't drop the UNIQUE constraint of oidc_subject, so the table is recreated, and existing users get a new account at their next sign-in
DROP TABLE users;
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  subject TEXT NOT NULL,
  email TEXT,
  name TEXT,
  created_at BIGINT NOT NULL,
  last_login_at BIGINT,
  picture TEXT,
  issuer TEXT NOT NULL,
  UNIQUE (issuer, subject)
);

-- +goose Down
DROP TABLE users;
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  oidc_subject TEXT NOT NULL UNIQUE,
  email TEXT,
  name TEXT,
  created_at BIGINT NOT NULL,
  last_login_at BIGINT,
  picture TEXT
);
