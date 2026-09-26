package auth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// Service handles sign-in, sessions, and users
type Service struct {
	db         *database.DB
	queries    *authdb.Queries
	appURL     string
	codec      *cookieCodec
	workspaces WorkspaceResolver

	// providers are in the order of the login page
	providers []configuredProvider
}

// configuredProvider pairs a provider's settings with the implementation of its type
type configuredProvider struct {
	ProviderConfig
	provider
}

func newService(db *database.DB, cfg Config, codec *cookieCodec, workspaces WorkspaceResolver) (*Service, error) {
	s := &Service{db: db, queries: authdb.New(db), appURL: cfg.AppURL, codec: codec, workspaces: workspaces}
	for _, pc := range cfg.Providers {
		p, err := newProvider(pc, s.queries)
		if err != nil {
			return nil, err
		}
		s.providers = append(s.providers, configuredProvider{ProviderConfig: pc, provider: p})
	}

	// The login page shows the primary provider first and the others by name
	slices.SortFunc(s.providers, func(a, b configuredProvider) int {
		if a.Primary != b.Primary {
			if a.Primary {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	return s, nil
}

// Providers returns the sign-in providers in the order of the login page
func (s *Service) Providers() []ProviderConfig {
	configs := make([]ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		configs = append(configs, p.ProviderConfig)
	}
	return configs
}

// provider looks up a sign-in provider by the ID in the login and callback URLs
func (s *Service) provider(id string) (configuredProvider, error) {
	if len(s.providers) == 0 {
		return configuredProvider{}, apperror.LoginNotConfigured()
	}
	for _, p := range s.providers {
		if p.ID == id {
			return p, nil
		}
	}
	return configuredProvider{}, apperror.NotFound("Sign-in provider")
}

// redirectURL is where the provider sends the browser back to
// Every provider has its own, so one provider's response can't be passed off as another's
func (s *Service) redirectURL(p configuredProvider) string {
	return s.appURL + "/api/auth/callback/" + p.ID
}

// BeginLogin returns the provider's sign-in URL and the signed login-state cookie
func (s *Service) BeginLogin(ctx context.Context, providerID, redirect string) (string, http.Cookie, error) {
	p, err := s.provider(providerID)
	if err != nil {
		return "", http.Cookie{}, err
	}

	state := loginState{
		Provider:  p.ID,
		State:     crypto.RandomToken(24),
		Nonce:     crypto.RandomToken(24),
		Verifier:  oauth2.GenerateVerifier(),
		Redirect:  safeRedirect(redirect),
		ExpiresAt: time.Now().Add(loginStateTTL).Unix(),
	}
	authURL, err := p.authCodeURL(ctx, s.redirectURL(p), state)
	if err != nil {
		return "", http.Cookie{}, err
	}

	value, err := s.codec.encode(kindLoginState, state)
	if err != nil {
		return "", http.Cookie{}, fmt.Errorf("failed to encode login state: %w", err)
	}
	return authURL, s.codec.cookie(loginCookieName, value, "/api/auth", loginStateTTL), nil
}

// FinishLogin validates the callback, upserts the user, and returns the session cookie and where to go next
func (s *Service) FinishLogin(ctx context.Context, providerID, loginCookie, code, returnedState string) (http.Cookie, string, error) {
	p, err := s.provider(providerID)
	if err != nil {
		return http.Cookie{}, "", err
	}

	// Check the state against the signed cookie to stop login CSRF, and the provider against the one the login started with so a response can't come back through another provider
	var state loginState
	err = s.codec.decode(kindLoginState, loginCookie, &state)
	if err != nil || state.State == "" || state.State != returnedState || time.Now().Unix() > state.ExpiresAt {
		return http.Cookie{}, "", apperror.LoginFailed(errors.New("login state is missing, expired or does not match"))
	}
	if state.Provider != p.ID {
		return http.Cookie{}, "", apperror.LoginFailed(fmt.Errorf("login started with provider %q but returned through %q", state.Provider, p.ID))
	}

	// The provider redeems the code and decides whether the account may sign in
	account, err := p.identify(ctx, s.redirectURL(p), code, state)
	if err != nil {
		return http.Cookie{}, "", err
	}
	return s.signIn(ctx, account, p.ID, state.Redirect)
}

// signIn records the account's login and starts its session in the workspace it lands in, returning where the browser goes next
func (s *Service) signIn(ctx context.Context, account identity, providerID, redirect string) (http.Cookie, string, error) {
	user, err := s.UpsertUser(ctx, account)
	if err != nil {
		return http.Cookie{}, "", err
	}

	// A deactivated user stays locked out whatever the provider says
	if user.DisabledAt != nil {
		return http.Cookie{}, "", apperror.AccountDisabled()
	}

	// Only an address the provider vouched for may pick up invites sent to it
	email := ""
	if user.EmailVerified && user.Email != nil {
		email = *user.Email
	}
	workspaceID, redirect, err := s.workspaces.ResolveLogin(ctx, workspaces.LoginInfo{UserID: user.ID, VerifiedEmail: email, Redirect: redirect})
	if err != nil {
		return http.Cookie{}, "", err
	}
	cookie, err := s.SessionCookie(user.ID, workspaceID, providerID, time.Now().Add(sessionTTL))
	if err != nil {
		return http.Cookie{}, "", err
	}
	return cookie, redirect, nil
}

// UpsertUser records a login of the account
func (s *Service) UpsertUser(ctx context.Context, account identity) (authdb.User, error) {
	user, err := s.queries.UpsertUser(ctx, authdb.UpsertUserParams{
		ID:            database.NewID(),
		Issuer:        account.Issuer,
		Subject:       account.Subject,
		Email:         nonEmpty(account.Email),
		EmailVerified: account.EmailVerified,
		Name:          nonEmpty(account.Name),
		Picture:       nonEmpty(safePictureURL(account.Picture)),
		IsAdmin:       account.Admin,
		Now:           database.Now(),
	})
	if err != nil {
		return authdb.User{}, fmt.Errorf("failed to upsert user: %w", err)
	}
	return user, nil
}

// SessionCookie issues a session for the user in the workspace that ends at expiresAt, remembering the sign-in provider they signed in with
func (s *Service) SessionCookie(userID, workspaceID, providerID string, expiresAt time.Time) (http.Cookie, error) {
	return s.codec.sessionCookie(userID, workspaceID, providerID, expiresAt)
}

// LogoutCookies returns the cookies that clear the session
func (s *Service) LogoutCookies() []http.Cookie {
	return []http.Cookie{s.codec.expired(SessionCookieName, "/")}
}

// VerifySession turns a session cookie value into a principal
// The cookie only names the user and workspace, and the database decides on every request what the user may still do there
func (s *Service) VerifySession(ctx context.Context, value string) (principal.Principal, error) {
	claims, err := s.codec.parseSession(value)
	if err != nil {
		return principal.Principal{}, apperror.NotSignedIn()
	}
	access, err := s.workspaces.Access(ctx, claims.WorkspaceID, claims.UserID)
	if err != nil {
		return principal.Principal{}, err
	}
	return principal.Principal{
		UserID:           claims.UserID,
		WorkspaceID:      claims.WorkspaceID,
		LoginProvider:    claims.Provider,
		SessionExpiresAt: claims.ExpiresAt,
		Role:             access.Role,
		InstanceAdmin:    access.InstanceAdmin,
	}, nil
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

// safePictureURL keeps only absolute http(s) URLs from the picture claim, since the browser loads it as an image source
func safePictureURL(picture string) string {
	u, err := url.Parse(picture)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return picture
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
