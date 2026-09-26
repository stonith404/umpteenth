//go:build unit

package database_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestWorkspaceMemberConstraints(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	workspaceID := testutil.SeedWorkspace(t, db)
	userID := testutil.SeedUser(t, db)

	// An invite is either a link or an email address, never both or neither
	invite := func(id string, email, tokenHash any) error {
		_, err := db.ExecContext(t.Context(), "INSERT INTO workspace_invites (id, workspace_id, role, email, token_hash, created_at, expires_at) VALUES ($1, $2, 'member', $3, $4, $5, $5)",
			id, workspaceID, email, tokenHash, database.Now())
		return err
	}
	require.NoError(t, invite("link", nil, "hash"))
	require.NoError(t, invite("email", "someone@example.com", nil))
	require.Error(t, invite("both", "other@example.com", "other-hash"))
	require.Error(t, invite("neither", nil, nil))

	// Members and invites go with their workspace, and memberships with their user
	testutil.SeedMember(t, db, workspaceID, userID, "owner")
	testutil.Exec(t, db, "DELETE FROM users WHERE id = $1", userID)
	var count int64
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM workspace_members").Scan(&count))
	require.Zero(t, count)
	testutil.Exec(t, db, "DELETE FROM workspaces WHERE id = $1", workspaceID)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM workspace_invites").Scan(&count))
	require.Zero(t, count)
}
