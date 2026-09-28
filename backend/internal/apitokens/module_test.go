//go:build unit

package apitokens

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

func seedAPIToken(t *testing.T, db *database.DB, workspaceID, createdBy string, expiresAt *int64) string {
	t.Helper()
	id := database.NewID()
	testutil.Exec(t, db, `INSERT INTO api_tokens (id, workspace_id, name, token_hash, created_by, created_at, expires_at)
		VALUES ($1, $2, 'Test token', $3, $4, $5, $6)`, id, workspaceID, "hash-"+id, createdBy, database.Now(), expiresAt)
	return id
}

func newTestModule(t *testing.T, db *database.DB) *Module {
	return New(Dependencies{DB: db, Roles: workspaces.New(workspaces.Dependencies{DB: db, Enabled: true})})
}

func TestValidateAPITokenIDRejectsRevokedOrExpiredTokens(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	workspaceID := testutil.SeedWorkspace(t, db)
	creator := testutil.SeedUser(t, db)
	testutil.SeedMember(t, db, workspaceID, creator, "member")
	m := newTestModule(t, db)

	activeUntil := database.Now() + time.Minute.Milliseconds()
	activeID := seedAPIToken(t, db, workspaceID, creator, &activeUntil)
	require.NoError(t, m.ValidateAPITokenID(t.Context(), workspaceID, activeID))
	otherWorkspaceID := testutil.SeedWorkspace(t, db)
	err := m.ValidateAPITokenID(t.Context(), otherWorkspaceID, activeID)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)

	testutil.Exec(t, db, "DELETE FROM api_tokens WHERE id = $1", activeID)
	err = m.ValidateAPITokenID(t.Context(), workspaceID, activeID)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)

	expiredAt := database.Now() - 1
	expiredID := seedAPIToken(t, db, workspaceID, creator, &expiredAt)
	err = m.ValidateAPITokenID(t.Context(), workspaceID, expiredID)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)
}

func TestTokensActWithTheirCreatorsRole(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	workspaceID := testutil.SeedWorkspace(t, db)
	creator := testutil.SeedUser(t, db)
	testutil.SeedMember(t, db, workspaceID, creator, "admin")
	m := newTestModule(t, db)

	// The token carries its creator's role, but stays a token without a user of its own
	token := TokenPrefix + "secret"
	id := database.NewID()
	testutil.Exec(t, db, `INSERT INTO api_tokens (id, workspace_id, name, token_hash, created_by, created_at) VALUES ($1, $2, 'Test token', $3, $4, $5)`,
		id, workspaceID, crypto.HashToken(token), creator, database.Now())
	p, err := m.ValidateAPIToken(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, principal.RoleAdmin, p.Role)
	require.Equal(t, creator, p.TokenCreatorID)
	require.Empty(t, p.UserID)

	// A new role applies to the next request
	testutil.Exec(t, db, "UPDATE workspace_members SET role = 'member' WHERE user_id = $1", creator)
	p, err = m.ValidateAPIToken(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, principal.RoleMember, p.Role)

	// Once the creator is out of the workspace, so is the token
	testutil.Exec(t, db, "DELETE FROM workspace_members WHERE user_id = $1", creator)
	_, err = m.ValidateAPIToken(t.Context(), token)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)
	err = m.ValidateAPITokenID(t.Context(), workspaceID, id)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)

	// A deactivated creator takes their tokens with them, even as a member
	testutil.SeedMember(t, db, workspaceID, creator, "member")
	testutil.Exec(t, db, "UPDATE users SET disabled_at = $1 WHERE id = $2", database.Now(), creator)
	_, err = m.ValidateAPIToken(t.Context(), token)
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), err)
}

func TestCreateRejectsExpiriesBrowsersCannotShow(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	workspaceID := testutil.SeedWorkspace(t, db)
	creator := testutil.SeedUser(t, db)
	testutil.SeedMember(t, db, workspaceID, creator, "member")
	m := newTestModule(t, db)
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: workspaceID, UserID: creator, Role: principal.RoleMember})

	// One millisecond past the largest date JavaScript can hold
	tooLate := int64(8_640_000_000_000_001)
	in := &createInput{}
	in.Body.Name = "Forever"
	in.Body.ExpiresAt = &tooLate
	_, err := m.create(ctx, in)
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)

	inAYear := database.Now() + (365 * 24 * time.Hour).Milliseconds()
	in.Body.ExpiresAt = &inAYear
	out, err := m.create(ctx, in)
	require.NoError(t, err)
	require.Equal(t, &inAYear, out.Body.APIToken.ExpiresAt)
}
