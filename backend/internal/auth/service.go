package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// Service handles OIDC login, sessions, and users
type Service struct {
	db         *database.DB
	queries    *authdb.Queries
	cfg        Config
	codec      *cookieCodec
	workspaces WorkspaceResolver

	// The discovered provider is cached after the first successful discovery, which is safe because it is immutable configuration
	providerMu sync.Mutex
	provider   *oidc.Provider
}

func newService(db *database.DB, cfg Config, codec *cookieCodec, workspaces WorkspaceResolver) *Service {
	return &Service{db: db, queries: authdb.New(db), cfg: cfg, codec: codec, workspaces: workspaces}
}

// oidcProvider discovers the identity provider lazily, so the app starts even when the IdP is briefly unreachable
func (s *Service) oidcProvider(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	if s.cfg.Issuer == "" || s.cfg.ClientID == "" {
		return nil, nil, apperror.OIDCNotConfigured()
	}

	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.provider == nil {
		discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(discoverCtx, s.cfg.Issuer)
		if err != nil {
			return nil, nil, apperror.Unavailable(err, "Identity provider is unreachable")
		}
		s.provider = provider
	}

	return s.provider, &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		RedirectURL:  s.cfg.AppURL + "/api/auth/callback",
		Endpoint:     s.provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}, nil
}

// BeginLogin returns the identity provider URL and the signed login-state cookie
func (s *Service) BeginLogin(ctx context.Context, redirect string) (string, http.Cookie, error) {
	_, oauthCfg, err := s.oidcProvider(ctx)
	if err != nil {
		return "", http.Cookie{}, err
	}

	state := loginState{
		State:     crypto.RandomToken(24),
		Nonce:     crypto.RandomToken(24),
		Verifier:  oauth2.GenerateVerifier(),
		Redirect:  safeRedirect(redirect),
		ExpiresAt: time.Now().Add(loginStateTTL).Unix(),
	}
	value, err := s.codec.encode(kindLoginState, state)
	if err != nil {
		return "", http.Cookie{}, fmt.Errorf("failed to encode login state: %w", err)
	}

	authURL := oauthCfg.AuthCodeURL(state.State, oidc.Nonce(state.Nonce), oauth2.S256ChallengeOption(state.Verifier))
	return authURL, s.codec.cookie(loginCookieName, value, "/api/auth", loginStateTTL), nil
}

// FinishLogin validates the callback, upserts the user, and returns the session cookie and where to go next
func (s *Service) FinishLogin(ctx context.Context, loginCookie, code, returnedState string) (http.Cookie, string, error) {
	provider, oauthCfg, err := s.oidcProvider(ctx)
	if err != nil {
		return http.Cookie{}, "", err
	}

	// Check the state against the signed cookie to stop login CSRF
	var state loginState
	err = s.codec.decode(kindLoginState, loginCookie, &state)
	if err != nil || state.State == "" || state.State != returnedState || time.Now().Unix() > state.ExpiresAt {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(errors.New("login state is missing, expired or does not match"))
	}

	// Exchange the code with the PKCE verifier and verify the ID token
	token, err := oauthCfg.Exchange(ctx, code, oauth2.VerifierOption(state.Verifier))
	if err != nil {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(fmt.Errorf("code exchange failed: %w", err))
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(errors.New("token response has no id_token"))
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(fmt.Errorf("invalid ID token: %w", err))
	}
	if idToken.Nonce != state.Nonce {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(errors.New("nonce mismatch"))
	}

	var claims struct {
		Email             string   `json:"email"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	err = idToken.Claims(&claims)
	if err != nil {
		return http.Cookie{}, "", apperror.OIDCLoginFailed(fmt.Errorf("invalid claims: %w", err))
	}

	// The identity provider decides who may use the client; oidc.allowed_groups optionally narrows it further
	if len(s.cfg.AllowedGroups) > 0 && !slices.ContainsFunc(claims.Groups, func(g string) bool { return slices.Contains(s.cfg.AllowedGroups, g) }) {
		return http.Cookie{}, "", apperror.Forbidden("Your account is not in an allowed group")
	}

	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	user, err := s.UpsertUser(ctx, idToken.Subject, claims.Email, name)
	if err != nil {
		return http.Cookie{}, "", err
	}

	cookie, err := s.SessionCookieFor(ctx, user.ID)
	if err != nil {
		return http.Cookie{}, "", err
	}
	return cookie, state.Redirect, nil
}

// UpsertUser records a login for the OIDC subject
func (s *Service) UpsertUser(ctx context.Context, subject, email, name string) (authdb.User, error) {
	user, err := s.queries.UpsertUser(ctx, authdb.UpsertUserParams{
		ID:          database.NewID(),
		OidcSubject: subject,
		Email:       nonEmpty(email),
		Name:        nonEmpty(name),
		Now:         database.Now(),
	})
	if err != nil {
		return authdb.User{}, fmt.Errorf("failed to upsert user: %w", err)
	}
	return user, nil
}

// SessionCookieFor issues a session for the user in their workspace
// v1 has a single workspace, later this resolves the user's memberships
func (s *Service) SessionCookieFor(ctx context.Context, userID string) (http.Cookie, error) {
	workspaceID, err := s.workspaces.DefaultWorkspaceID(ctx)
	if err != nil {
		return http.Cookie{}, err
	}
	return s.codec.sessionCookie(userID, workspaceID)
}

// LogoutCookies returns the cookies that clear the session
func (s *Service) LogoutCookies() []http.Cookie {
	return []http.Cookie{s.codec.expired(SessionCookieName, "/")}
}

// VerifySession turns a session cookie value into a principal
func (s *Service) VerifySession(_ context.Context, value string) (principal.Principal, error) {
	claims, err := s.codec.parseSession(value)
	if err != nil {
		return principal.Principal{}, apperror.NotSignedIn()
	}
	return principal.Principal{UserID: claims.UserID, WorkspaceID: claims.WorkspaceID}, nil
}

// GetUser loads a user by ID
func (s *Service) GetUser(ctx context.Context, id string) (authdb.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if database.IsNotFound(err) {
		return authdb.User{}, apperror.NotFound("User")
	} else if err != nil {
		return authdb.User{}, fmt.Errorf("failed to load user: %w", err)
	}
	return user, nil
}

// safeRedirect only allows same-origin relative paths, so the login flow cannot be used as an open redirect
func safeRedirect(redirect string) string {
	if redirect == "" || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") || strings.HasPrefix(redirect, "/\\") {
		return "/"
	}
	if u, err := url.Parse(redirect); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return redirect
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
