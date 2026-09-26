//go:build unit

package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

const testAppURL = "https://umpteenth.example.com"

// fakeIssuer is an OpenID provider that signs in whoever it is told to, as a user approving the login would
type fakeIssuer struct {
	*httptest.Server

	key *rsa.PrivateKey

	mu sync.Mutex
	// Subject and Groups are the user the next authorization signs in
	Subject string
	Groups  []string
	// EmailVerified is the email_verified claim, left out when nil
	EmailVerified any
	// codes maps an issued authorization code to the nonce of its request
	codes map[string]string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIssuer{key: key, Subject: "subject", codes: map[string]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                f.URL,
			"authorization_endpoint":                f.URL + "/authorize",
			"token_endpoint":                        f.URL + "/token",
			"jwks_uri":                              f.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		nonce, ok := f.codes[r.FormValue("code")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(f.codes, r.FormValue("code"))
		clientID, _, _ := r.BasicAuth()
		writeJSON(w, map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": f.idToken(t, clientID, nonce)})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// authorize approves the authorization request behind the URL and returns the code the callback receives
func (f *fakeIssuer) authorize(t *testing.T, authURL string) string {
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, f.URL+"/authorize", u.Scheme+"://"+u.Host+u.Path)

	f.mu.Lock()
	defer f.mu.Unlock()
	code := "code-" + u.Query().Get("state")
	f.codes[code] = u.Query().Get("nonce")
	return code
}

func (f *fakeIssuer) idToken(t *testing.T, clientID, nonce string) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: "key"}}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	claims := map[string]any{
		"iss":    f.URL,
		"sub":    f.Subject,
		"aud":    clientID,
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
		"nonce":  nonce,
		"email":  "user@example.com",
		"name":   "User",
		"groups": f.Groups,
	}
	if f.EmailVerified != nil {
		claims["email_verified"] = f.EmailVerified
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	signed, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := signed.CompactSerialize()
	require.NoError(t, err)
	return token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newTestService(t *testing.T, providers ...ProviderConfig) *Service {
	// Sign-ins land in the one shared workspace, as they do with workspaces turned off
	db := testutil.NewDatabaseForTest(t)
	ws := workspaces.New(workspaces.Dependencies{DB: db})
	_, err := ws.EnsureDefault(t.Context(), false)
	require.NoError(t, err)

	m, err := New(Dependencies{
		DB:            db,
		Workspaces:    ws,
		EncryptionKey: []byte("unit-test-encryption-key"),
		Config:        Config{AppURL: testAppURL, Providers: providers},
	})
	require.NoError(t, err)
	return m.service
}

// authorizer approves the authorization request behind a URL and returns the code the callback receives, as a fake provider does
type authorizer interface {
	authorize(t *testing.T, authURL string) string
}

// signIn runs a whole login through the provider and returns the user of the session it ends with
func signIn(t *testing.T, svc *Service, providerID string, issuer authorizer) (string, error) {
	authURL, loginCookie, err := svc.BeginLogin(t.Context(), providerID, "/jobs")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)

	session, redirect, err := svc.FinishLogin(t.Context(), providerID, loginCookie.Value, issuer.authorize(t, authURL), u.Query().Get("state"))
	if err != nil {
		return "", err
	}
	require.Equal(t, "/jobs", redirect)
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)
	require.Equal(t, providerID, p.LoginProvider)
	return p.UserID, nil
}

func TestProvidersListThePrimaryFirstAndTheOthersByName(t *testing.T) {
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "zitadel", Name: "zitadel"},
		ProviderConfig{Type: TypeOIDC, ID: "google", Name: "Google"},
		ProviderConfig{Type: TypeOIDC, ID: "pocket-id", Name: "Pocket ID", Primary: true},
	)

	var ids []string
	for _, p := range svc.Providers() {
		ids = append(ids, p.ID)
	}
	require.Equal(t, []string{"pocket-id", "google", "zitadel"}, ids)
}

func TestLoginRedirectsToTheChosenProvider(t *testing.T) {
	a, b := newFakeIssuer(t), newFakeIssuer(t)
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a"},
		ProviderConfig{Type: TypeOIDC, ID: "b", Name: "B", Issuer: b.URL, ClientID: "client-b"},
	)

	authURL, _, err := svc.BeginLogin(t.Context(), "b", "/")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, b.URL+"/authorize", u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, "client-b", u.Query().Get("client_id"))
	require.Equal(t, testAppURL+"/api/auth/callback/b", u.Query().Get("redirect_uri"))
}

func TestLoginRejectsUnknownProviders(t *testing.T) {
	_, _, err := newTestService(t).BeginLogin(t.Context(), "a", "/")
	require.True(t, apperror.IsCode(err, apperror.CodeLoginNotConfigured))

	_, _, err = newTestService(t, ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A"}).BeginLogin(t.Context(), "b", "/")
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound))
}

func TestSubjectsAreUsersPerIssuer(t *testing.T) {
	a, b := newFakeIssuer(t), newFakeIssuer(t)
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a"},
		ProviderConfig{Type: TypeOIDC, ID: "b", Name: "B", Issuer: b.URL, ClientID: "client-b"},
	)

	// Signing in again with the same provider finds the same user
	first, err := signIn(t, svc, "a", a)
	require.NoError(t, err)
	again, err := signIn(t, svc, "a", a)
	require.NoError(t, err)
	require.Equal(t, first, again)

	// The same subject at another issuer is someone else
	other, err := signIn(t, svc, "b", b)
	require.NoError(t, err)
	require.NotEqual(t, first, other)
}

func TestCallbackOfAnotherProviderCantFinishALogin(t *testing.T) {
	a, b := newFakeIssuer(t), newFakeIssuer(t)
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a"},
		ProviderConfig{Type: TypeOIDC, ID: "b", Name: "B", Issuer: b.URL, ClientID: "client-b"},
	)

	// A login started with one provider must not be finished by the callback of another, which would send its code to the wrong token endpoint
	authURL, loginCookie, err := svc.BeginLogin(t.Context(), "a", "/")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	_, _, err = svc.FinishLogin(t.Context(), "b", loginCookie.Value, a.authorize(t, authURL), u.Query().Get("state"))
	require.True(t, apperror.IsCode(err, apperror.CodeLoginFailed), "got %v", err)
}

func TestAllowedGroupsApplyPerProvider(t *testing.T) {
	a, b := newFakeIssuer(t), newFakeIssuer(t)
	a.Groups, b.Groups = []string{"dev"}, []string{"dev"}
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a", AllowedGroups: []string{"admins"}},
		ProviderConfig{Type: TypeOIDC, ID: "b", Name: "B", Issuer: b.URL, ClientID: "client-b"},
	)

	_, err := signIn(t, svc, "a", a)
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden), "got %v", err)
	_, err = signIn(t, svc, "b", b)
	require.NoError(t, err)
}

func TestAdminGroupsMakeInstanceAdminsAtEverySignIn(t *testing.T) {
	a := newFakeIssuer(t)
	svc := newTestService(t, ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a", AllowedGroups: []string{"dev"}, AdminGroups: []string{"ops"}})

	// Admins get in even when allowed_groups doesn't list their group
	a.Groups = []string{"ops"}
	userID, err := signIn(t, svc, "a", a)
	require.NoError(t, err)
	user, err := svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.True(t, user.IsAdmin)

	// Leaving the group takes the admin role away at the next sign-in
	a.Groups = []string{"dev"}
	_, err = signIn(t, svc, "a", a)
	require.NoError(t, err)
	user, err = svc.GetUser(t.Context(), userID)
	require.NoError(t, err)
	require.False(t, user.IsAdmin)
}

func TestEmailAddressesAreOnlyVerifiedWhenTheProviderSaysSo(t *testing.T) {
	a := newFakeIssuer(t)
	svc := newTestService(t, ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a"})

	// Some providers send the claim as a string, and a missing claim vouches for nothing
	for _, c := range []struct {
		claim any
		want  bool
	}{{nil, false}, {false, false}, {true, true}, {"true", true}, {"false", false}} {
		a.EmailVerified = c.claim
		userID, err := signIn(t, svc, "a", a)
		require.NoError(t, err)
		user, err := svc.GetUser(t.Context(), userID)
		require.NoError(t, err)
		require.Equal(t, c.want, user.EmailVerified, "email_verified %v", c.claim)
	}
}

func TestDeactivatedUsersAreLockedOut(t *testing.T) {
	a := newFakeIssuer(t)
	svc := newTestService(t, ProviderConfig{Type: TypeOIDC, ID: "a", Name: "A", Issuer: a.URL, ClientID: "client-a"})
	authURL, loginCookie, err := svc.BeginLogin(t.Context(), "a", "/")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	session, _, err := svc.FinishLogin(t.Context(), "a", loginCookie.Value, a.authorize(t, authURL), u.Query().Get("state"))
	require.NoError(t, err)
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)

	// The existing session stops working at once, and signing in again is refused
	testutil.Exec(t, svc.db, "UPDATE users SET disabled_at = $1 WHERE id = $2", time.Now().UnixMilli(), p.UserID)
	_, err = svc.VerifySession(t.Context(), session.Value)
	require.True(t, apperror.IsCode(err, apperror.CodeNotSignedIn), "got %v", err)
	_, err = signIn(t, svc, "a", a)
	require.True(t, apperror.IsCode(err, apperror.CodeAccountDisabled), "got %v", err)
}
