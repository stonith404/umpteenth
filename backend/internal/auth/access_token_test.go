//go:build unit

package auth

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// mcpAudience is the resource the fake issuer issues MCP access tokens for, the MCP endpoint of the test app
const mcpAudience = testAppURL + "/api/mcp"

// accessToken signs an access token for the MCP endpoint as the user the issuer signs in, with claims replacing or adding to the defaults
func (f *fakeIssuer) accessToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	all := map[string]any{
		"iss":   f.URL,
		"sub":   f.Subject,
		"aud":   mcpAudience,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"scope": "",
	}
	maps.Copy(all, claims)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: "key"}}, (&jose.SignerOptions{}).WithType("at+jwt"))
	require.NoError(t, err)
	payload, err := json.Marshal(all)
	require.NoError(t, err)
	signed, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := signed.CompactSerialize()
	require.NoError(t, err)
	return token
}

// newAccessTokenService is a service whose MCP clients sign in through the issuer, with several workspaces when enabled
func newAccessTokenService(t *testing.T, f *fakeIssuer, enabled bool, provider ProviderConfig) (*Service, *workspaces.Module) {
	t.Helper()
	db := testutil.NewDatabaseForTest(t)
	ws := workspaces.New(workspaces.Dependencies{DB: db, Enabled: enabled})
	require.NoError(t, ws.EnsureDefault(t.Context(), false))

	provider.Type, provider.ID, provider.Name, provider.Issuer, provider.ClientID = TypeOIDC, "pocket-id", "Pocket ID", f.URL, "umpteenth"
	m, err := New(Dependencies{
		DB:            db,
		Workspaces:    ws,
		EncryptionKey: []byte("unit-test-encryption-key"),
		Config: Config{
			AppURL:       testAppURL,
			Providers:    []ProviderConfig{provider},
			AccessTokens: &AccessTokenConfig{ProviderID: "pocket-id", Audience: mcpAudience},
		},
	})
	require.NoError(t, err)
	return m.service, ws
}

// requireInvalidToken expects a refused access token, with the message the MCP client shows when one is given
func requireInvalidToken(t *testing.T, err error, message string) {
	t.Helper()
	require.True(t, apperror.IsCode(err, apperror.CodeInvalidToken), "got %v", err)
	if message != "" {
		appErr, _ := apperror.As(err)
		require.Contains(t, appErr.Message(), message)
	}
}

func TestAnAccessTokenActsAsTheUserWhoSignedInBefore(t *testing.T) {
	f := newFakeIssuer(t)
	svc, _ := newAccessTokenService(t, f, false, ProviderConfig{})

	// Signing in once in the browser creates the account the token acts as
	_, _, err := svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	requireInvalidToken(t, err, "Sign in to Umpteenth in the browser once")
	userID, err := signIn(t, svc, "pocket-id", f)
	require.NoError(t, err)

	p, expiry, err := svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	require.NoError(t, err)
	assert.Equal(t, principal.Principal{WorkspaceID: workspaces.DefaultID, Credential: principal.CredentialOAuth, UserID: userID, LoginProvider: "pocket-id", Role: principal.RoleOwner}, p)
	assert.WithinDuration(t, time.Now().Add(time.Hour), expiry, time.Minute)
	assert.False(t, p.IsSession())
}

func TestAccessTokensMustBeIssuedForTheMCPEndpoint(t *testing.T) {
	f := newFakeIssuer(t)
	svc, _ := newAccessTokenService(t, f, false, ProviderConfig{})
	_, err := signIn(t, svc, "pocket-id", f)
	require.NoError(t, err)

	// The ID token of Umpteenth's own sign-in client names the client as its audience, so it can't stand in for an access token
	requireToken := func(token string) {
		t.Helper()
		_, _, err := svc.VerifyAccessToken(t.Context(), token, "")
		requireInvalidToken(t, err, "")
	}
	requireToken(f.idToken(t, "umpteenth", "nonce"))
	requireToken(f.accessToken(t, map[string]any{"aud": "https://other.example.com/mcp"}))
	requireToken(f.accessToken(t, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}))
	requireToken(f.accessToken(t, map[string]any{"iss": "https://evil.example.com"}))
	requireToken(f.accessToken(t, map[string]any{"sub": ""}))

	// A token signed by another key or not signed at all is refused too
	other := newFakeIssuer(t)
	other.Subject = f.Subject
	forged := other.accessToken(t, map[string]any{"iss": f.URL})
	requireToken(forged)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"at+jwt"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + f.URL + `","sub":"subject","aud":"` + mcpAudience + `","exp":` + "9999999999" + `}`))
	requireToken(header + "." + payload + ".")
}

func TestAnAccessTokenOfADeactivatedUserOrOneOutsideTheAllowedGroupsIsRefused(t *testing.T) {
	f := newFakeIssuer(t)
	f.Groups = []string{"dev"}
	svc, _ := newAccessTokenService(t, f, false, ProviderConfig{AllowedGroups: []string{"dev"}})
	userID, err := signIn(t, svc, "pocket-id", f)
	require.NoError(t, err)

	// A token that lists the user's groups is checked against the allowed groups again, and one without them was checked at sign-in
	_, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, map[string]any{"groups": []string{"marketing"}}), "")
	requireInvalidToken(t, err, "not in an allowed group")
	_, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, map[string]any{"groups": []string{"dev"}}), "")
	require.NoError(t, err)
	_, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	require.NoError(t, err)

	testutil.Exec(t, svc.db, "UPDATE users SET disabled_at = $1 WHERE id = $2", database.Now(), userID)
	_, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	requireInvalidToken(t, err, "deactivated")
}

func TestAnAccessTokenWorksInTheWorkspaceTheClientNamesWithTheMembershipRole(t *testing.T) {
	f := newFakeIssuer(t)
	svc, _ := newAccessTokenService(t, f, true, ProviderConfig{AdminGroups: []string{"ops"}})

	// The user is an instance admin, who owns the default workspace and is a member of a second one
	f.Groups = []string{"ops"}
	userID, err := signIn(t, svc, "pocket-id", f)
	require.NoError(t, err)
	team := testutil.SeedWorkspace(t, svc.db)
	testutil.SeedMember(t, svc.db, team, userID, "member")
	other := testutil.SeedWorkspace(t, svc.db)

	// Without a header the client works where the user last worked in the browser
	p, _, err := svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	require.NoError(t, err)
	assert.Equal(t, workspaces.DefaultID, p.WorkspaceID)
	assert.Equal(t, principal.RoleOwner, p.Role)

	// A named workspace counts with the membership role, and being an instance admin opens no other workspace
	p, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), team)
	require.NoError(t, err)
	assert.Equal(t, team, p.WorkspaceID)
	assert.Equal(t, principal.RoleMember, p.Role)
	assert.False(t, p.InstanceAdmin)
	_, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), other)
	requireInvalidToken(t, err, "no access to this workspace")

	// Once the user leaves the workspace they last worked in, the client falls back to their oldest one
	testutil.Exec(t, svc.db, "UPDATE users SET last_workspace_id = $1 WHERE id = $2", other, userID)
	p, _, err = svc.VerifyAccessToken(t.Context(), f.accessToken(t, nil), "")
	require.NoError(t, err)
	assert.Equal(t, workspaces.DefaultID, p.WorkspaceID)
}
