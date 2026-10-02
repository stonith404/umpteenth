package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// AccessTokenConfig names the OpenID Connect provider MCP clients sign in with, and the audience its access tokens must be issued for
type AccessTokenConfig struct {
	ProviderID string
	// Audience is the URL of the MCP endpoint, which the provider puts into the tokens it issues for that resource
	Audience string
}

// accessTokenProvider returns the provider MCP clients sign in with, and false when they can only use API tokens
func (s *Service) accessTokenProvider() (configuredProvider, bool) {
	if s.accessTokens == nil {
		return configuredProvider{}, false
	}
	p, err := s.provider(s.accessTokens.ProviderID)
	if err != nil {
		return configuredProvider{}, false
	}
	return p, true
}

// VerifyAccessToken resolves an access token an MCP client got from the identity provider to the user who signed in
// The client works in the workspace it names, or else where the user last worked, with their membership role and never an instance admin's
func (s *Service) VerifyAccessToken(ctx context.Context, raw, workspaceID string) (principal.Principal, time.Time, error) {
	p, ok := s.accessTokenProvider()
	if !ok {
		return principal.Principal{}, time.Time{}, apperror.InvalidToken()
	}
	oidcP, ok := p.provider.(*oidcProvider)
	if !ok {
		return principal.Principal{}, time.Time{}, fmt.Errorf("sign-in provider %q issues no access tokens", p.ID)
	}
	discovered, err := oidcP.discover(ctx)
	if err != nil {
		return principal.Principal{}, time.Time{}, err
	}

	// The audience is the MCP endpoint, so the ID tokens the provider issues to Umpteenth's own sign-in client never pass
	token, err := discovered.Verifier(&oidc.Config{ClientID: s.accessTokens.Audience}).Verify(ctx, raw)
	if err != nil {
		slog.DebugContext(ctx, "Rejected an MCP access token", slog.String("provider", p.ID), slog.Any("error", err))
		return principal.Principal{}, time.Time{}, apperror.InvalidToken()
	}
	if token.Subject == "" {
		return principal.Principal{}, time.Time{}, apperror.InvalidToken()
	}

	// The allowed groups were checked at sign-in, and a token that lists the user's groups is checked again in case they left
	var claims struct {
		Groups *[]string `json:"groups"`
	}
	err = token.Claims(&claims)
	if err != nil {
		return principal.Principal{}, time.Time{}, apperror.InvalidToken()
	}
	if claims.Groups != nil && len(p.AllowedGroups) > 0 {
		inGroups := func(groups []string) bool {
			return slices.ContainsFunc(*claims.Groups, func(g string) bool { return slices.Contains(groups, g) })
		}
		if !inGroups(p.AllowedGroups) && !inGroups(p.AdminGroups) {
			return principal.Principal{}, time.Time{}, apperror.New(apperror.CodeInvalidToken, http.StatusUnauthorized, "Your account is not in an allowed group")
		}
	}

	// Signing in to Umpteenth creates the account, which an MCP client never does, so a user who never signed in has none yet
	userID, err := s.queries.GetUserByIdentity(ctx, authdb.GetUserByIdentityParams{Issuer: token.Issuer, Subject: token.Subject})
	if database.IsNotFound(err) {
		return principal.Principal{}, time.Time{}, apperror.New(apperror.CodeInvalidToken, http.StatusUnauthorized, "Sign in to Umpteenth in the browser once before you connect an agent")
	} else if err != nil {
		return principal.Principal{}, time.Time{}, fmt.Errorf("failed to load the user: %w", err)
	}

	resolved, role, err := s.workspaces.AgentAccess(ctx, userID, workspaceID)
	if err != nil {
		return principal.Principal{}, time.Time{}, err
	}
	return principal.Principal{
		WorkspaceID:   resolved,
		Credential:    principal.CredentialOAuth,
		UserID:        userID,
		LoginProvider: p.ID,
		Role:          role,
	}, token.Expiry, nil
}
