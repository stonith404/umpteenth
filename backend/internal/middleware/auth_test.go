//go:build unit

package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

type tokenValidatorStub struct {
	validatedToken string
	workspaceID    string
	tokenID        string
}

func (s *tokenValidatorStub) ValidateAPIToken(_ context.Context, token string) (principal.Principal, error) {
	s.validatedToken = token
	return principal.Principal{WorkspaceID: "workspace-1", TokenID: "token-1"}, nil
}

func (s *tokenValidatorStub) ValidateAPITokenID(_ context.Context, workspaceID, tokenID string) error {
	s.workspaceID = workspaceID
	s.tokenID = tokenID
	return apperror.InvalidToken()
}

type sessionVerifierStub struct {
	calls int
}

func (s *sessionVerifierStub) VerifySession(_ context.Context, _ string) (principal.Principal, error) {
	s.calls++
	if s.calls == 1 {
		return principal.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}, nil
	}
	return principal.Principal{}, apperror.NotSignedIn()
}

func TestRequiredAttachesBearerCredentialRevalidation(t *testing.T) {
	tokens := &tokenValidatorStub{}
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{
		OperationID: "test-bearer-revalidation",
		Method:      http.MethodGet,
		Path:        "/test",
		Middlewares: NewAuth(nil, tokens, "session", "https://example.com").Required(),
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := RevalidateCredential(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	})

	resp := api.Get("/test", "Authorization: Bearer ump_test")
	require.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
	require.Equal(t, "ump_test", tokens.validatedToken)
	require.Equal(t, "workspace-1", tokens.workspaceID)
	require.Equal(t, "token-1", tokens.tokenID)
}

func TestRequiredAttachesCookieCredentialRevalidation(t *testing.T) {
	sessions := &sessionVerifierStub{}
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{
		OperationID: "test-cookie-revalidation",
		Method:      http.MethodGet,
		Path:        "/test",
		Middlewares: NewAuth(sessions, nil, "session", "https://example.com").Required(),
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := RevalidateCredential(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	})

	resp := api.Get("/test", "Cookie: session=session-value")
	require.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
	require.Equal(t, 2, sessions.calls)
}

// fixedSessions signs every cookie in as the same principal
type fixedSessions struct{ p principal.Principal }

func (s fixedSessions) VerifySession(context.Context, string) (principal.Principal, error) {
	return s.p, nil
}

// fixedTokens accepts every bearer token as the same principal
type fixedTokens struct{ p principal.Principal }

func (s fixedTokens) ValidateAPIToken(context.Context, string) (principal.Principal, error) {
	return s.p, nil
}
func (s fixedTokens) ValidateAPITokenID(context.Context, string, string) error { return nil }

func TestRequiredEnforcesTheOperationsAccessRule(t *testing.T) {
	member := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialSession, UserID: "user", Role: principal.RoleMember}
	admin := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialSession, UserID: "user", Role: principal.RoleAdmin}
	owner := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialSession, UserID: "user", Role: principal.RoleOwner}
	instanceAdmin := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialSession, UserID: "user", Role: principal.RoleOwner, InstanceAdmin: true}
	oauthUser := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialOAuth, UserID: "user", Role: principal.RoleAdmin}
	adminToken := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialAPIToken, TokenID: "token", TokenCreatorID: "user", Role: principal.RoleAdmin}

	cases := []struct {
		name   string
		access httpserver.Access
		caller principal.Principal
		token  bool
		status int
	}{
		{"any member", httpserver.Access{}, member, false, http.StatusNoContent},
		{"member below admin", httpserver.Access{MinRole: principal.RoleAdmin}, member, false, http.StatusForbidden},
		{"admin", httpserver.Access{MinRole: principal.RoleAdmin}, admin, false, http.StatusNoContent},
		{"owner above admin", httpserver.Access{MinRole: principal.RoleAdmin}, owner, false, http.StatusNoContent},
		{"admin below owner", httpserver.Access{MinRole: principal.RoleOwner}, admin, false, http.StatusForbidden},
		{"token of an admin", httpserver.Access{MinRole: principal.RoleAdmin}, adminToken, true, http.StatusNoContent},
		{"token where a session is needed", httpserver.Access{SessionOnly: true}, adminToken, true, http.StatusForbidden},
		{"OAuth access token where a session is needed", httpserver.Access{SessionOnly: true}, oauthUser, true, http.StatusForbidden},
		{"OAuth access token of an admin", httpserver.Access{MinRole: principal.RoleAdmin}, oauthUser, true, http.StatusNoContent},
		{"workspace owner who isn't an instance admin", httpserver.Access{InstanceAdmin: true}, owner, false, http.StatusForbidden},
		{"instance admin", httpserver.Access{InstanceAdmin: true, SessionOnly: true}, instanceAdmin, false, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, api := humatest.New(t)
			op := httpserver.Restrict(huma.Operation{OperationID: "test-access", Method: http.MethodGet, Path: "/test"}, c.access)
			httpserver.Register(api, op, NewAuth(fixedSessions{c.caller}, fixedTokens{c.caller}, "session", "https://example.com").Required(), func(context.Context, *struct{}) (*struct{}, error) {
				return nil, nil
			})

			credential := "Cookie: session=value"
			if c.token {
				credential = "Authorization: Bearer ump_test"
			}
			resp := api.Get("/test", credential)
			require.Equal(t, c.status, resp.Code, resp.Body.String())
		})
	}
}

// rejectingTokens fails every bearer token, which shows whether a request took the token path
type rejectingTokens struct{ calls int }

func (s *rejectingTokens) ValidateAPIToken(context.Context, string) (principal.Principal, error) {
	s.calls++
	return principal.Principal{}, apperror.InvalidToken()
}
func (s *rejectingTokens) ValidateAPITokenID(context.Context, string, string) error { return nil }

func TestRequiredFallsBackToTheCookieForNonBearerAuthorization(t *testing.T) {
	member := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialSession, UserID: "user", Role: principal.RoleMember}
	tokens := &rejectingTokens{}
	_, api := humatest.New(t)
	auth := NewAuth(fixedSessions{member}, tokens, "session", "https://example.com").Required()
	httpserver.Register(api, huma.Operation{OperationID: "test-get", Method: http.MethodGet, Path: "/test"}, auth, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, nil
	})
	httpserver.Register(api, huma.Operation{OperationID: "test-post", Method: http.MethodPost, Path: "/test"}, auth, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, nil
	})

	// A reverse proxy with HTTP Basic auth passes the browser's cached credentials along with the session cookie
	resp := api.Get("/test", "Cookie: session=value", "Authorization: Basic dXNlcjpwYXNz")
	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())
	resp = api.Post("/test", "Cookie: session=value", "Authorization: Basic dXNlcjpwYXNz", "Sec-Fetch-Site: same-origin")
	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())

	// Falling back to the cookie keeps the cross-site check that guards cookie-authenticated writes
	resp = api.Post("/test", "Cookie: session=value", "Authorization: Basic dXNlcjpwYXNz", "Sec-Fetch-Site: cross-site")
	require.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())

	// Without a cookie a non-Bearer header is still no credential
	resp = api.Get("/test", "Authorization: Basic dXNlcjpwYXNz")
	require.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
	require.Contains(t, resp.Body.String(), "invalid_token")

	// A bearer token is still validated and a bad one is not rescued by the cookie
	resp = api.Get("/test", "Cookie: session=value", "Authorization: Bearer ump_bad")
	require.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
	require.Equal(t, 1, tokens.calls)
}

func TestRequiredTrustsACallerAuthenticatedInProcess(t *testing.T) {
	caller := principal.Principal{WorkspaceID: "ws", Credential: principal.CredentialAPIToken, TokenID: "token", TokenCreatorID: "user", Role: principal.RoleMember}
	_, api := humatest.New(t)
	auth := NewAuth(nil, nil, "session", "https://example.com").Required()
	var seen principal.Principal
	huma.Register(api, huma.Operation{OperationID: "test-member", Method: http.MethodPost, Path: "/member", Middlewares: auth},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			seen, _ = principal.From(ctx)
			return nil, RevalidateCredential(ctx)
		})
	huma.Register(api, httpserver.Restrict(huma.Operation{OperationID: "test-admin", Method: http.MethodPost, Path: "/admin", Middlewares: auth}, httpserver.Access{MinRole: principal.RoleAdmin}),
		func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })

	revalidated := false
	ctx := WithAuthenticated(t.Context(), caller, func(context.Context) error {
		revalidated = true
		return nil
	})

	// A cross-site write passes, since an in-process caller never comes with a cookie, and the credential stays revalidatable
	resp := api.PostCtx(ctx, "/member", "Sec-Fetch-Site: cross-site", "X-Umpteenth-Workspace: other")
	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())
	require.Equal(t, caller, seen)
	require.True(t, revalidated)

	// The operation's access rule still applies
	resp = api.PostCtx(ctx, "/admin")
	require.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())

	// Without the context value the same request has no credentials at all
	resp = api.Post("/member")
	require.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
}
