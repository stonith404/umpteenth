//go:build unit

package database_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestWorkspaceMembersMigration(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	provider, err := database.MigrationProvider(db)
	require.NoError(t, err)

	// Step back to before workspace members, where a token can have lost its creator
	_, err = provider.DownTo(t.Context(), 9)
	require.NoError(t, err)
	workspaceID := testutil.SeedWorkspace(t, db)
	userID := testutil.SeedUser(t, db)
	insertToken := func(id string, createdBy any) {
		testutil.Exec(t, db, "INSERT INTO api_tokens (id, workspace_id, name, token_hash, created_by, created_at) VALUES ($1, $2, 'Token', $3, $4, $5)",
			id, workspaceID, "hash-"+id, createdBy, database.Now())
	}
	insertToken("with-creator", userID)
	insertToken("without-creator", nil)

	// Tokens without a creator can't act with anyone's role, so the migration removes them
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	var ids []string
	rows, err := db.QueryContext(t.Context(), "SELECT id FROM api_tokens")
	require.NoError(t, err)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Close())
	require.Equal(t, []string{"with-creator"}, ids)

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
