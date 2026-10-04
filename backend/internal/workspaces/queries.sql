-- name: EnsureWorkspace :exec
INSERT INTO workspaces (id, name, created_at) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: EnsureWorkspaceIfNone :exec
-- Only a fresh instance gets the default workspace, so one that was deleted doesn't come back
INSERT INTO workspaces (id, name, created_at)
SELECT sqlc.arg(id), sqlc.arg(name), sqlc.arg(created_at) WHERE NOT EXISTS (SELECT 1 FROM workspaces)
ON CONFLICT (id) DO NOTHING;

-- name: CreateWorkspace :exec
INSERT INTO workspaces (id, name, created_at) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(created_at));

-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = sqlc.arg(id);

-- name: RenameWorkspace :execrows
UPDATE workspaces SET name = sqlc.arg(name) WHERE id = sqlc.arg(id);

-- name: DeleteWorkspace :execrows
DELETE FROM workspaces WHERE id = sqlc.arg(id);

-- name: GetAccess :one
-- One query answers every authenticated request: whether the user may still sign in, is an instance admin, and which role they have in the workspace
SELECT u.is_admin, u.disabled_at, m.role, (SELECT COUNT(*) FROM workspaces w WHERE w.id = sqlc.arg(workspace_id)) AS workspace_count
FROM users u
LEFT JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = sqlc.arg(workspace_id)
WHERE u.id = sqlc.arg(user_id);

-- name: GetLoginUser :one
-- unscoped: users are instance-wide
SELECT name, email, last_workspace_id FROM users WHERE id = sqlc.arg(id);

-- name: LockUser :exec
-- unscoped: users are instance-wide
-- Writing the row makes concurrent sign-ins of the same user wait for each other on Postgres, where SQLite serializes writes anyway
UPDATE users SET last_login_at = last_login_at WHERE id = sqlc.arg(id);

-- name: SetLastWorkspace :exec
-- unscoped: users are instance-wide
UPDATE users SET last_workspace_id = sqlc.arg(workspace_id) WHERE id = sqlc.arg(id);

-- name: CountMembersWithEmail :one
-- Only members of the workspace count, whose addresses its member list shows anyway, so an invite never tells whether an address has an account elsewhere
SELECT COUNT(*) FROM workspace_members m JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = sqlc.arg(workspace_id) AND LOWER(u.email) = sqlc.arg(email) AND u.email_verified = TRUE;

-- name: AddMember :execrows
-- A user who is a member already keeps their role, and the unique owner index makes a second owner a no-op instead of an error that would abort the transaction
INSERT INTO workspace_members (workspace_id, user_id, role, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(user_id), sqlc.arg(role), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: ClaimOwnerless :execrows
-- The first member of a workspace without an owner becomes its owner, which is how a fresh instance gets one
INSERT INTO workspace_members (workspace_id, user_id, role, created_at)
SELECT w.id, sqlc.arg(user_id), 'owner', sqlc.arg(created_at) FROM workspaces w
WHERE w.id = sqlc.arg(workspace_id) AND NOT EXISTS (SELECT 1 FROM workspace_members o WHERE o.workspace_id = w.id AND o.role = 'owner')
ON CONFLICT DO NOTHING;

-- name: GetMemberRole :one
SELECT role FROM workspace_members WHERE workspace_id = sqlc.arg(workspace_id) AND user_id = sqlc.arg(user_id);

-- name: DemoteOwner :exec
UPDATE workspace_members SET role = 'admin' WHERE workspace_id = sqlc.arg(workspace_id) AND role = 'owner';

-- name: PromoteToOwner :execrows
UPDATE workspace_members SET role = 'owner' WHERE workspace_id = sqlc.arg(workspace_id) AND user_id = sqlc.arg(user_id);

-- name: SetMemberRole :execrows
-- The owner only changes through a handover, and leaving them out here rather than in a check before keeps a handover at the same time from being undone
UPDATE workspace_members SET role = sqlc.arg(role) WHERE workspace_id = sqlc.arg(workspace_id) AND user_id = sqlc.arg(user_id) AND role <> 'owner';

-- name: RemoveMember :execrows
-- The owner can't be removed, which like above holds against a handover at the same time
DELETE FROM workspace_members WHERE workspace_id = sqlc.arg(workspace_id) AND user_id = sqlc.arg(user_id) AND role <> 'owner';

-- name: CountUserMemberships :one
-- unscoped: counts the user's memberships across workspaces
SELECT COUNT(*) FROM workspace_members WHERE user_id = sqlc.arg(user_id);

-- name: OldestMembership :one
-- unscoped: looks across the user's workspaces for the one to land in
SELECT workspace_id FROM workspace_members WHERE user_id = sqlc.arg(user_id) ORDER BY created_at, workspace_id LIMIT 1;

-- name: ListUserWorkspaces :many
-- unscoped: lists the user's workspaces across the instance
SELECT w.id, w.name, m.role FROM workspace_members m
JOIN workspaces w ON w.id = m.workspace_id
WHERE m.user_id = sqlc.arg(user_id)
ORDER BY LOWER(w.name), w.id;

-- name: CreateLinkInvite :exec
INSERT INTO workspace_invites (id, workspace_id, role, token_hash, created_by, created_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(role), sqlc.arg(token_hash), sqlc.narg(created_by), sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: UpsertEmailInvite :exec
-- Inviting an address again refreshes the invite instead of failing
INSERT INTO workspace_invites (id, workspace_id, role, email, created_by, created_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(role), sqlc.arg(email), sqlc.narg(created_by), sqlc.arg(created_at), sqlc.arg(expires_at))
ON CONFLICT (workspace_id, email) DO UPDATE SET role = excluded.role, created_by = excluded.created_by, created_at = excluded.created_at, expires_at = excluded.expires_at;

-- name: ListInvites :many
SELECT i.id, i.role, i.email, i.created_at, i.expires_at, u.name AS created_by_name, u.email AS created_by_email
FROM workspace_invites i
LEFT JOIN users u ON u.id = i.created_by
WHERE i.workspace_id = sqlc.arg(workspace_id)
ORDER BY i.created_at DESC, i.id;

-- name: DeleteInvite :execrows
DELETE FROM workspace_invites WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetInviteByTokenHash :one
-- unscoped: the token hash is what identifies the invite and its workspace
SELECT i.id, i.workspace_id, i.role, i.expires_at, w.name AS workspace_name, u.name AS created_by_name, u.email AS created_by_email
FROM workspace_invites i
JOIN workspaces w ON w.id = i.workspace_id
LEFT JOIN users u ON u.id = i.created_by
WHERE i.token_hash = sqlc.arg(token_hash) AND u.disabled_at IS NULL AND (u.is_admin OR EXISTS (
 SELECT 1 FROM workspace_members m WHERE m.workspace_id = i.workspace_id AND m.user_id = u.id AND m.role IN ('admin', 'owner')
));

-- name: ListEmailInvites :many
-- unscoped: finds the invites to the user's verified address in every workspace
SELECT i.id, i.workspace_id, i.role FROM workspace_invites i JOIN users u ON u.id = i.created_by
WHERE i.email = sqlc.arg(email) AND i.expires_at > sqlc.arg(now) AND u.disabled_at IS NULL AND (u.is_admin OR EXISTS (
 SELECT 1 FROM workspace_members m WHERE m.workspace_id = i.workspace_id AND m.user_id = u.id AND m.role IN ('admin', 'owner')
)) ORDER BY i.created_at, i.id;

-- name: RevokeMemberInvites :exec
DELETE FROM workspace_invites WHERE workspace_id = sqlc.arg(workspace_id) AND created_by = sqlc.arg(user_id);
