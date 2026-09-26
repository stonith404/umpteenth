//go:build unit

package auth

import (
	"cmp"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// fakeGitHub is GitHub's OAuth endpoints and REST API for one account, which approves every authorization as a user clicking authorize would
type fakeGitHub struct {
	*httptest.Server

	mu sync.Mutex
	// User is the account that signs in, as GET /user returns it
	User map[string]any
	// Emails is the account's address list, which holds its private addresses
	Emails []map[string]any
	// Orgs maps an organization to the state of the account's membership in it
	Orgs map[string]string
	// OrgIDs maps an organization name to the numeric ID of the organization that holds it, 1 when unset
	OrgIDs map[string]int
	// scope is the scope the last authorization asked for
	scope string
	codes map[string]bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{
		User:   map[string]any{"id": 42, "login": "octocat", "name": "", "email": nil, "avatar_url": "https://avatars.example.com/42"},
		Emails: []map[string]any{{"email": "old@example.com", "primary": false, "verified": true}, {"email": "octocat@example.com", "primary": true, "verified": true}},
		Orgs:   map[string]string{},
		OrgIDs: map[string]int{},
		codes:  map[string]bool{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.codes[r.FormValue("code")] || r.FormValue("code_verifier") == "" {
			writeJSON(w, map[string]string{"error": "bad_verification_code"})
			return
		}
		delete(f.codes, r.FormValue("code"))
		writeJSON(w, map[string]string{"access_token": "gho_token", "token_type": "bearer", "scope": f.scope})
	})
	api := func(handler func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer gho_token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			handler(w, r)
		}
	}
	mux.HandleFunc("GET /user", api(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, f.User) }))
	mux.HandleFunc("GET /user/emails", api(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, f.Emails) }))
	mux.HandleFunc("GET /user/memberships/orgs/{org}", api(func(w http.ResponseWriter, r *http.Request) {
		state, ok := f.Orgs[r.PathValue("org")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"state": state, "organization": map[string]any{"login": r.PathValue("org"), "id": cmp.Or(f.OrgIDs[r.PathValue("org")], 1)}})
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) authorize(t *testing.T, authURL string) string {
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, f.URL+"/login/oauth/authorize", u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, "S256", u.Query().Get("code_challenge_method"))

	f.mu.Lock()
	defer f.mu.Unlock()
	f.scope = u.Query().Get("scope")
	code := "code-" + u.Query().Get("state")
	f.codes[code] = true
	return code
}

// newGitHubTestService signs in through the fake instead of GitHub
func newGitHubTestService(t *testing.T, f *fakeGitHub, cfg ProviderConfig) *Service {
	cfg.ID, cfg.Type, cfg.Name, cfg.ClientID, cfg.ClientSecret = "github", TypeGitHub, "GitHub", "client", "secret"
	svc := newTestService(t, cfg)
	p := svc.providers[0].provider.(*githubProvider)
	p.webURL, p.apiURL = f.URL, f.URL
	return svc
}

func TestGitHubSignsInAllowedUsers(t *testing.T) {
	f := newFakeGitHub(t)
	svc := newGitHubTestService(t, f, ProviderConfig{AllowedUsers: []string{"OctoCat"}})

	// Usernames match regardless of case, and only the email scope is needed without organizations
	userID, err := signIn(t, svc, "github", f)
	require.NoError(t, err)
	require.Equal(t, "user:email", f.scope)

	// The numeric ID is the subject, a private primary address is the email, and the username stands in for a missing name
	user, err := svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.Equal(t, f.URL, user.Issuer)
	require.Equal(t, "42", user.Subject)
	require.Equal(t, "octocat@example.com", *user.Email)
	require.Equal(t, "octocat", *user.Name)
	require.Equal(t, "https://avatars.example.com/42", *user.Picture)

	// A renamed account keeps its user, since the subject doesn't change
	f.User["login"], f.User["name"], f.User["email"] = "OctoCat", "The Octocat", "public@example.com"
	again, err := signIn(t, svc, "github", f)
	require.NoError(t, err)
	require.Equal(t, userID, again)
	user, err = svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.Equal(t, "public@example.com", *user.Email)
	require.Equal(t, "The Octocat", *user.Name)
}

func TestGitHubSignsInActiveOrganizationMembers(t *testing.T) {
	f := newFakeGitHub(t)
	svc := newGitHubTestService(t, f, ProviderConfig{AllowedOrganizations: []string{"acme", "initech"}})

	// A pending invitation isn't a membership yet
	f.Orgs["acme"] = "pending"
	_, err := signIn(t, svc, "github", f)
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "got %v", err)
	require.ElementsMatch(t, []string{"user:email", "read:org"}, strings.Fields(f.scope))

	f.Orgs["initech"] = "active"
	_, err = signIn(t, svc, "github", f)
	require.NoError(t, err)
}

func TestGitHubRejectsOtherAccounts(t *testing.T) {
	f := newFakeGitHub(t)
	f.Orgs["other"] = "active"
	svc := newGitHubTestService(t, f, ProviderConfig{AllowedUsers: []string{"someone-else"}, AllowedOrganizations: []string{"acme"}})

	_, err := signIn(t, svc, "github", f)
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "got %v", err)
}

func TestGitHubAdminsAreLetInAndMadeInstanceAdmins(t *testing.T) {
	f := newFakeGitHub(t)
	svc := newGitHubTestService(t, f, ProviderConfig{AllowedUsers: []string{"someone-else"}, AdminOrganizations: []string{"acme"}})

	// A member of an admin organization gets in without being allowed otherwise, and becomes an instance admin with a verified address
	f.Orgs["acme"] = "active"
	userID, err := signIn(t, svc, "github", f)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"user:email", "read:org"}, strings.Fields(f.scope))
	user, err := svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.True(t, user.IsAdmin)
	require.True(t, user.EmailVerified)

	// An admin username works the same without asking GitHub about organizations
	f.Orgs["acme"] = "pending"
	svc = newGitHubTestService(t, f, ProviderConfig{AdminUsers: []string{"octocat"}})
	userID, err = signIn(t, svc, "github", f)
	require.NoError(t, err)
	user, err = svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.True(t, user.IsAdmin)
}

func TestGitHubRefusesAnotherAccountHoldingAListedUsername(t *testing.T) {
	for name, cfg := range map[string]ProviderConfig{
		"admin_users":   {AdminUsers: []string{"alice"}},
		"allowed_users": {AllowedUsers: []string{"alice"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t)
			svc := newGitHubTestService(t, f, cfg)

			// Alice signs in with her account under the listed username
			f.User["id"], f.User["login"] = 42, "alice"
			aliceID, err := signIn(t, svc, "github", f)
			require.NoError(t, err)

			// Alice renames her account, and GitHub frees the old username for anyone to register
			// A different account that claimed it must not inherit the access the list gave her
			f.User["id"], f.User["login"] = 1337, "alice"
			f.User["email"] = "attacker@example.com"
			attackerID, err := signIn(t, svc, "github", f)
			require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "account 1337 signed in as user %q next to alice's %q: %v", attackerID, aliceID, err)

			// Alice's own account keeps signing in under the listed username
			f.User["id"], f.User["login"] = 42, "alice"
			again, err := signIn(t, svc, "github", f)
			require.NoError(t, err)
			require.Equal(t, aliceID, again)
		})
	}
}

func TestGitHubRefusesMembersOfAnOrganizationRegisteredUnderAListedName(t *testing.T) {
	f := newFakeGitHub(t)
	svc := newGitHubTestService(t, f, ProviderConfig{AdminOrganizations: []string{"acme"}})

	// A member of the listed organization signs in
	f.Orgs["acme"], f.OrgIDs["acme"] = "active", 100
	_, err := signIn(t, svc, "github", f)
	require.NoError(t, err)

	// The organization renames itself, and someone else registers the freed name as a new organization
	f.User["id"], f.User["login"] = 1337, "mallory"
	f.Orgs["acme"], f.OrgIDs["acme"] = "active", 666
	attackerID, err := signIn(t, svc, "github", f)
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "a member of the new organization acme signed in as user %q: %v", attackerID, err)

	// Members of the organization that held the name first keep signing in
	f.User["id"], f.User["login"] = 42, "octocat"
	f.OrgIDs["acme"] = 100
	_, err = signIn(t, svc, "github", f)
	require.NoError(t, err)
}
