//go:build unit

package workspaces

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// newTestModule creates the module on a fresh database, recording which workspaces it seeds
func newTestModule(t *testing.T, enabled bool) (*Module, *database.DB, *[]string) {
	t.Helper()
	return newTestModuleOn(t, testutil.NewDatabaseForTest(t), enabled)
}

func newTestModuleOn(t *testing.T, db *database.DB, enabled bool) (*Module, *database.DB, *[]string) {
	t.Helper()
	m := New(Dependencies{DB: db, Enabled: enabled, AppURL: "https://umpteenth.example.com"})
	var mu sync.Mutex
	seeded := &[]string{}
	m.SetSeeder(func(_ context.Context, wid string) error {
		mu.Lock()
		defer mu.Unlock()
		*seeded = append(*seeded, wid)
		return nil
	})
	err := m.EnsureDefault(t.Context(), enabled)
	require.NoError(t, err)
	return m, db, seeded
}

// seedUser creates a user with a verified email address, as a sign-in would
func seedUser(t *testing.T, db *database.DB, name, email string) string {
	t.Helper()
	id := database.NewID()
	testutil.Exec(t, db, "INSERT INTO users (id, issuer, subject, email, email_verified, name, created_at) VALUES ($1, 'https://issuer.test', $2, $3, TRUE, $4, $5)",
		id, id, email, name, database.Now())
	return id
}

func signIn(t *testing.T, m *Module, userID, email, redirect string) (string, string) {
	t.Helper()
	wid, next, err := m.ResolveLogin(t.Context(), LoginInfo{UserID: userID, VerifiedEmail: email, Redirect: redirect})
	require.NoError(t, err)
	return wid, next
}

func roleOf(t *testing.T, m *Module, workspaceID, userID string) principal.Role {
	t.Helper()
	access, err := m.Access(t.Context(), workspaceID, userID)
	require.NoError(t, err)
	return access.Role
}

func requireCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.True(t, apperror.IsCode(err, code), "want %s, got %v", code, err)
}

func TestWithWorkspacesOffEveryoneSharesTheDefaultWorkspace(t *testing.T) {
	m, db, seeded := newTestModule(t, false)
	first := seedUser(t, db, "First", "first@example.com")
	second := seedUser(t, db, "Second", "second@example.com")

	// The first user to sign in owns the workspace and everyone after joins as a member
	wid, redirect := signIn(t, m, first, "", "/jobs")
	require.Equal(t, DefaultID, wid)
	require.Equal(t, "/jobs", redirect)
	wid, _ = signIn(t, m, second, "", "/")
	require.Equal(t, DefaultID, wid)
	require.Equal(t, principal.RoleOwner, roleOf(t, m, DefaultID, first))
	require.Equal(t, principal.RoleMember, roleOf(t, m, DefaultID, second))
	require.Empty(t, *seeded)

	// Signing in again keeps the role an admin gave
	require.NoError(t, m.SetRole(t.Context(), DefaultID, second, principal.RoleAdmin))
	signIn(t, m, second, "", "/")
	require.Equal(t, principal.RoleAdmin, roleOf(t, m, DefaultID, second))

	// Everything that needs a second workspace is refused, and sessions naming another workspace no longer work
	other := testutil.SeedWorkspace(t, db)
	testutil.SeedMember(t, db, other, first, "owner")
	_, err := m.Access(t.Context(), other, first)
	requireCode(t, err, apperror.CodeNotSignedIn)
	_, err = m.Create(t.Context(), first, "Another")
	requireCode(t, err, apperror.CodeForbidden)
	_, err = m.Invite(t.Context(), DefaultID, first, "", principal.RoleMember, time.Hour)
	requireCode(t, err, apperror.CodeForbidden)
	requireCode(t, m.Leave(t.Context(), DefaultID, second), apperror.CodeForbidden)
	requireCode(t, m.Remove(t.Context(), DefaultID, second), apperror.CodeForbidden)
	requireCode(t, m.Delete(t.Context(), DefaultID), apperror.CodeForbidden)
}

func TestTheFirstUserOwnsAFreshInstanceAndEveryoneElseGetsAPersonalWorkspace(t *testing.T) {
	m, db, seeded := newTestModule(t, true)
	first := seedUser(t, db, "First", "first@example.com")
	second := seedUser(t, db, "Ada Lovelace", "ada@example.com")

	// The default workspace of a fresh instance goes to its first user, and was seeded at start already
	wid, _ := signIn(t, m, first, "", "/")
	require.Equal(t, DefaultID, wid)
	require.Equal(t, principal.RoleOwner, roleOf(t, m, DefaultID, first))

	// The next user gets a workspace of their own, which is seeded now
	wid, _ = signIn(t, m, second, "", "/")
	require.NotEqual(t, DefaultID, wid)
	require.Equal(t, principal.RoleOwner, roleOf(t, m, wid, second))
	ws, err := m.Get(t.Context(), wid)
	require.NoError(t, err)
	require.Equal(t, "Ada Lovelace's workspace", ws.Name)
	require.Equal(t, []string{wid}, *seeded)
	_, err = m.Access(t.Context(), DefaultID, second)
	requireCode(t, err, apperror.CodeNotSignedIn)

	// Signing in again lands in the same workspace instead of creating another
	again, _ := signIn(t, m, second, "", "/")
	require.Equal(t, wid, again)
	require.Len(t, *seeded, 1)
}

func TestTheDefaultWorkspaceOnlyComesBackWithWorkspacesOff(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	_, err := m.Create(t.Context(), owner, "Other")
	require.NoError(t, err)
	require.NoError(t, m.Delete(t.Context(), DefaultID))

	defaultExists := func() bool {
		var n int64
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM workspaces WHERE id = $1", DefaultID).Scan(&n))
		return n > 0
	}
	require.NoError(t, m.EnsureDefault(t.Context(), true))
	require.False(t, defaultExists(), "a deleted default workspace must not come back while other workspaces exist")
	require.NoError(t, m.EnsureDefault(t.Context(), false))
	require.True(t, defaultExists())
}

func TestConcurrentFirstSignInsLeaveOneOwner(t *testing.T) {
	// An in-memory SQLite database fails fast on locks instead of waiting like a real one, so SQLite runs this on a file
	db := testutil.NewDatabaseForTest(t)
	if db.Engine() == database.EngineSQLite {
		var err error
		db, err = database.Open(t.Context(), database.EngineSQLite, filepath.Join(t.TempDir(), "test.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		require.NoError(t, database.Migrate(t.Context(), db))
	}
	m, db, _ := newTestModuleOn(t, db, true)
	users := make([]string, 6)
	for i := range users {
		users[i] = seedUser(t, db, "User", "")
	}

	// Everyone races for the ownerless default workspace, and each loser gets a personal workspace
	var wg sync.WaitGroup
	errs := make(chan error, len(users))
	for _, u := range users {
		wg.Go(func() {
			_, _, err := m.ResolveLogin(context.Background(), LoginInfo{UserID: u})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var owners, workspaces int64
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner'", DefaultID).Scan(&owners))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM workspaces").Scan(&workspaces))
	require.EqualValues(t, 1, owners)
	require.EqualValues(t, len(users), workspaces)
}

func TestEmailInvitesWaitForAVerifiedSignIn(t *testing.T) {
	m, db, seeded := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")

	// An address nobody signed in with yet waits, and inviting it again refreshes the invite
	res, err := m.Invite(t.Context(), DefaultID, owner, "New@Example.com ", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	require.False(t, res.MemberAdded)
	_, err = m.Invite(t.Context(), DefaultID, owner, "new@example.com", principal.RoleAdmin, time.Hour)
	require.NoError(t, err)
	invites, err := m.queries.ListInvites(t.Context(), DefaultID)
	require.NoError(t, err)
	require.Len(t, invites, 1)

	// Someone whose provider didn't vouch for the address can't pick it up, and gets a personal workspace instead
	impostor := seedUser(t, db, "Impostor", "new@example.com")
	wid, _ := signIn(t, m, impostor, "", "/")
	require.NotEqual(t, DefaultID, wid)

	// A verified sign-in with the address joins with the invite's role, lands there, and needs no personal workspace
	invited := seedUser(t, db, "Invited", "other@example.com")
	seededBefore := len(*seeded)
	wid, _ = signIn(t, m, invited, "NEW@example.com", "/")
	require.Equal(t, DefaultID, wid)
	require.Equal(t, principal.RoleAdmin, roleOf(t, m, DefaultID, invited))
	require.Len(t, *seeded, seededBefore)
	invites, err = m.queries.ListInvites(t.Context(), DefaultID)
	require.NoError(t, err)
	require.Empty(t, invites)

	// An expired invite is ignored
	_, err = m.Invite(t.Context(), DefaultID, owner, "late@example.com", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	testutil.Exec(t, db, "UPDATE workspace_invites SET expires_at = $1", database.Now()-1)
	late := seedUser(t, db, "Late", "late@example.com")
	wid, _ = signIn(t, m, late, "late@example.com", "/")
	require.NotEqual(t, DefaultID, wid)
}

func TestInvitingAKnownVerifiedAddressAddsThePersonRightAway(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	known := seedUser(t, db, "Known", "known@example.com")

	res, err := m.Invite(t.Context(), DefaultID, owner, "KNOWN@example.com", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	require.True(t, res.MemberAdded)
	require.Equal(t, principal.RoleMember, roleOf(t, m, DefaultID, known))
	_, err = m.Invite(t.Context(), DefaultID, owner, "known@example.com", principal.RoleMember, time.Hour)
	requireCode(t, err, apperror.CodeConflict)

	// Two accounts with the same address are ambiguous, so the invite waits for a sign-in instead
	seedUser(t, db, "Twin", "twin@example.com")
	seedUser(t, db, "Twin", "twin@example.com")
	res, err = m.Invite(t.Context(), DefaultID, owner, "twin@example.com", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	require.False(t, res.MemberAdded)

	// Nothing but an address and the admin or member role is accepted
	_, err = m.Invite(t.Context(), DefaultID, owner, "not an address", principal.RoleMember, time.Hour)
	requireCode(t, err, apperror.CodeValidationFailed)
	_, err = m.Invite(t.Context(), DefaultID, owner, "", principal.RoleOwner, time.Hour)
	requireCode(t, err, apperror.CodeValidationFailed)
}

func TestInviteLinksAreUsedOnce(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	res, err := m.Invite(t.Context(), DefaultID, owner, "", principal.RoleAdmin, time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, res.Token)

	// A member opening the link doesn't use it up, so it still reaches the person it was meant for
	preview, err := m.LookupInvite(t.Context(), res.Token, owner)
	require.NoError(t, err)
	require.True(t, preview.AlreadyMember)
	require.Equal(t, principal.RoleAdmin, preview.Role)
	_, err = m.AcceptInvite(t.Context(), res.Token, owner)
	require.NoError(t, err)
	require.Equal(t, principal.RoleOwner, roleOf(t, m, DefaultID, owner))

	// The first new person joins, and after that the link is gone
	joiner := seedUser(t, db, "Joiner", "joiner@example.com")
	signIn(t, m, joiner, "", "/")
	wid, err := m.AcceptInvite(t.Context(), res.Token, joiner)
	require.NoError(t, err)
	require.Equal(t, DefaultID, wid)
	require.Equal(t, principal.RoleAdmin, roleOf(t, m, DefaultID, joiner))
	latecomer := seedUser(t, db, "Latecomer", "latecomer@example.com")
	_, err = m.AcceptInvite(t.Context(), res.Token, latecomer)
	requireCode(t, err, apperror.CodeNotFound)

	// An expired link can still be looked at but not used
	res, err = m.Invite(t.Context(), DefaultID, owner, "", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	testutil.Exec(t, db, "UPDATE workspace_invites SET expires_at = $1", database.Now()-1)
	preview, err = m.LookupInvite(t.Context(), res.Token, latecomer)
	require.NoError(t, err)
	require.True(t, preview.Expired)
	_, err = m.AcceptInvite(t.Context(), res.Token, latecomer)
	requireCode(t, err, apperror.CodeConflict)
}

func TestASignInFromAnInviteLinkJoinsThatWorkspace(t *testing.T) {
	m, db, seeded := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	res, err := m.Invite(t.Context(), DefaultID, owner, "", principal.RoleMember, time.Hour)
	require.NoError(t, err)

	// The new user lands in the invited workspace without a personal one, and the browser goes there instead of back to the invite page
	newcomer := seedUser(t, db, "Newcomer", "newcomer@example.com")
	wid, redirect := signIn(t, m, newcomer, "", InvitePath+res.Token)
	require.Equal(t, DefaultID, wid)
	require.Equal(t, "/", redirect)
	require.Empty(t, *seeded)

	// An unknown invite keeps the redirect, so the invite page can say what went wrong
	stranger := seedUser(t, db, "Stranger", "stranger@example.com")
	_, redirect = signIn(t, m, stranger, "", InvitePath+"unknown")
	require.Equal(t, InvitePath+"unknown", redirect)
}

func TestASignInFromAnInviteLinkDoesntMoveSomeoneWhoHasAWorkspace(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	victim := seedUser(t, db, "Victim", "victim@example.com")
	home, _ := signIn(t, m, victim, "", "/")
	attacker := seedUser(t, db, "Attacker", "attacker@example.com")
	attackerWorkspace, _ := signIn(t, m, attacker, "", "/")
	require.NotEqual(t, home, attackerWorkspace)
	res, err := m.Invite(t.Context(), attackerWorkspace, attacker, "", principal.RoleMember, time.Hour)
	require.NoError(t, err)

	// Any page can start a login with an invite redirect in a signed-in user's browser, so it must leave them where they were and let the invite page ask
	wid, redirect := signIn(t, m, victim, "", InvitePath+res.Token)
	require.Equal(t, home, wid, "the login moved the session into the inviting workspace")
	require.Equal(t, InvitePath+res.Token, redirect, "the invite page's confirmation was skipped")
	_, err = m.Access(t.Context(), attackerWorkspace, victim)
	requireCode(t, err, apperror.CodeNotSignedIn)

	// The next sign-in still lands in the user's own workspace
	wid, _ = signIn(t, m, victim, "", "/")
	require.Equal(t, home, wid)
}

func TestAnEmailInviteDoesntMoveSomeoneWhoHasAWorkspace(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	victim := seedUser(t, db, "Victim", "victim@example.com")
	home, _ := signIn(t, m, victim, "victim@example.com", "/")
	attacker := seedUser(t, db, "Attacker", "attacker@example.com")
	attackerWorkspace, _ := signIn(t, m, attacker, "", "/")

	// A second account with the same address, such as one through another sign-in provider, keeps the invite waiting for a sign-in
	seedUser(t, db, "Victim elsewhere", "victim@example.com")
	res, err := m.Invite(t.Context(), attackerWorkspace, attacker, "victim@example.com", principal.RoleMember, time.Hour)
	require.NoError(t, err)
	require.False(t, res.MemberAdded)

	// Like an invite that adds a known user right away, picking it up at a sign-in leaves the user in the workspace they were in
	wid, _ := signIn(t, m, victim, "victim@example.com", "/")
	require.Equal(t, home, wid, "the sign-in moved the session into the inviting workspace")
	require.Equal(t, principal.RoleMember, roleOf(t, m, attackerWorkspace, victim))
	wid, _ = signIn(t, m, victim, "victim@example.com", "/")
	require.Equal(t, home, wid)
}

func TestOwnershipOnlyChangesHands(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	member := seedUser(t, db, "Member", "member@example.com")
	signIn(t, m, owner, "", "/")
	testutil.SeedMember(t, db, DefaultID, member, "member")

	// Nobody can be made owner, and the owner can't be demoted, removed or leave
	requireCode(t, m.SetRole(t.Context(), DefaultID, member, principal.RoleOwner), apperror.CodeValidationFailed)
	requireCode(t, m.SetRole(t.Context(), DefaultID, owner, principal.RoleMember), apperror.CodeForbidden)
	requireCode(t, m.Remove(t.Context(), DefaultID, owner), apperror.CodeForbidden)
	requireCode(t, m.Leave(t.Context(), DefaultID, owner), apperror.CodeConflict)

	// A second owner can't exist at all
	_, err := db.ExecContext(t.Context(), "UPDATE workspace_members SET role = 'owner' WHERE user_id = $1", member)
	require.True(t, database.IsUniqueViolation(err), "got %v", err)

	// Handing over makes the previous owner an admin
	require.NoError(t, m.Transfer(t.Context(), DefaultID, member))
	require.Equal(t, principal.RoleOwner, roleOf(t, m, DefaultID, member))
	require.Equal(t, principal.RoleAdmin, roleOf(t, m, DefaultID, owner))
	requireCode(t, m.Transfer(t.Context(), DefaultID, seedUser(t, db, "Outsider", "")), apperror.CodeNotFound)

	// The previous owner can leave now, and lands in a personal workspace since it was their only one
	require.NoError(t, m.Leave(t.Context(), DefaultID, owner))
	next, err := m.relocate(t.Context(), owner)
	require.NoError(t, err)
	require.NotEqual(t, DefaultID, next)
	require.Equal(t, principal.RoleOwner, roleOf(t, m, next, owner))
}

func TestInstanceAdminsActAsOwnersEverywhereButNotThroughTokens(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	admin := seedUser(t, db, "Admin", "admin@example.com")
	testutil.Exec(t, db, "UPDATE users SET is_admin = TRUE WHERE id = $1", admin)

	access, err := m.Access(t.Context(), DefaultID, admin)
	require.NoError(t, err)
	require.Equal(t, Access{Role: principal.RoleOwner, InstanceAdmin: true}, access)
	require.NoError(t, m.Switch(t.Context(), DefaultID, admin))
	_, err = m.TokenAccess(t.Context(), DefaultID, admin)
	requireCode(t, err, apperror.CodeInvalidToken)

	// A deactivated user loses access even as an owner, and a missing workspace can't be switched to
	testutil.Exec(t, db, "UPDATE users SET disabled_at = $1 WHERE id = $2", database.Now(), owner)
	_, err = m.Access(t.Context(), DefaultID, owner)
	requireCode(t, err, apperror.CodeNotSignedIn)
	requireCode(t, m.Switch(t.Context(), database.NewID(), admin), apperror.CodeNotFound)
}

type cleanupStub struct {
	err   error
	after chan string
}

func (c cleanupStub) BeforeDelete(_ context.Context, workspaceID string) (func(context.Context), error) {
	if c.err != nil {
		return nil, c.err
	}
	return func(context.Context) { c.after <- workspaceID }, nil
}

func TestDeletingAWorkspaceGoesThroughItsCleanup(t *testing.T) {
	m, db, _ := newTestModule(t, true)
	owner := seedUser(t, db, "Owner", "owner@example.com")
	signIn(t, m, owner, "", "/")
	wid, err := m.Create(t.Context(), owner, "Doomed")
	require.NoError(t, err)
	_, err = m.Invite(t.Context(), wid, owner, "someone@example.com", principal.RoleMember, time.Hour)
	require.NoError(t, err)

	// A refusal, such as active runs, keeps the workspace
	m.SetCleanup(cleanupStub{err: apperror.Conflict("busy")})
	requireCode(t, m.Delete(t.Context(), wid), apperror.CodeConflict)
	_, err = m.Get(t.Context(), wid)
	require.NoError(t, err)

	// Otherwise the rows go with the workspace, and the files after it
	after := make(chan string, 1)
	m.SetCleanup(cleanupStub{after: after})
	require.NoError(t, m.Delete(t.Context(), wid))
	select {
	case got := <-after:
		require.Equal(t, wid, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the cleanup after the delete never ran")
	}
	var rows int64
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT (SELECT COUNT(*) FROM workspace_members WHERE workspace_id = $1) + (SELECT COUNT(*) FROM workspace_invites WHERE workspace_id = $1)", wid).Scan(&rows))
	require.Zero(t, rows)
	requireCode(t, m.Delete(t.Context(), wid), apperror.CodeNotFound)
}

func TestPersonalWorkspacesAreNamedAfterTheirOwner(t *testing.T) {
	name := func(s string) *string { return &s }
	require.Equal(t, "Ada's workspace", personalName(name(" Ada "), name("ada@example.com")))
	require.Equal(t, "ada's workspace", personalName(nil, name("ada@example.com")))
	require.Equal(t, "Personal workspace", personalName(name(""), nil))
	long := personalName(name(strings.Repeat("x", 200)), nil)
	require.Len(t, []rune(long), maxNameLength)
	require.True(t, strings.HasSuffix(long, "'s workspace"))
}
