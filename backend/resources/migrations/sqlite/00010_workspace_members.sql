-- +goose Up
-- Workspaces get members with roles and invites, and users can be instance admins or be deactivated
-- is_admin and email_verified come from the sign-in provider and are refreshed at every sign-in
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN disabled_at BIGINT;
-- The next sign-in lands in the workspace the user was in last, and it has no foreign key since membership is checked whenever it is used
ALTER TABLE users ADD COLUMN last_workspace_id TEXT;

CREATE TABLE workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
  created_at BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user ON workspace_members (user_id);
-- A workspace has at most one owner, which also settles two sign-ins racing to claim a workspace without one
CREATE UNIQUE INDEX workspace_members_owner ON workspace_members (workspace_id) WHERE role = 'owner';

-- An invite is either a link, whose token only the person it was sent to knows, or an email address matched at sign-in
CREATE TABLE workspace_invites (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
  email TEXT,
  token_hash TEXT UNIQUE,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  CHECK ((email IS NULL) <> (token_hash IS NULL)),
  UNIQUE (workspace_id, email)
);
CREATE INDEX workspace_invites_email ON workspace_invites (email);

-- A token acts with the role of the user who created it, so a token whose creator is gone can't act at all
DELETE FROM api_tokens WHERE created_by IS NULL;

-- +goose Down
DROP TABLE workspace_invites;
DROP TABLE workspace_members;
ALTER TABLE users DROP COLUMN last_workspace_id;
ALTER TABLE users DROP COLUMN disabled_at;
ALTER TABLE users DROP COLUMN email_verified;
ALTER TABLE users DROP COLUMN is_admin;
