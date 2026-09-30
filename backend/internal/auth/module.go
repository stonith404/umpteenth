package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// WorkspaceResolver picks the workspace a user signs in to and decides what they may do there
type WorkspaceResolver interface {
	ResolveLogin(ctx context.Context, info workspaces.LoginInfo) (workspaceID, redirect string, err error)
	Access(ctx context.Context, workspaceID, userID string) (workspaces.Access, error)
	Get(ctx context.Context, workspaceID string) (workspaces.Summary, error)
	Enabled() bool
}

// UsageUnits tells which unit a workspace shows usage in, price or tokens
type UsageUnits interface {
	UsageUnit(ctx context.Context, workspaceID string) (string, error)
}

// Config holds the ways users can sign in
type Config struct {
	AppURL    string
	Providers []ProviderConfig
}

// ProviderConfig is a way users can sign in, whose Type picks the implementation
type ProviderConfig struct {
	// ID names the provider in its login and callback URLs
	ID           string
	Type         string
	Name         string
	Icon         string
	Primary      bool
	ClientID     string
	ClientSecret string
	// Issuer, AllowedGroups and AdminGroups configure an OpenID Connect provider
	Issuer        string
	AllowedGroups []string
	AdminGroups   []string
	// AllowedUsers and AllowedOrganizations name the GitHub accounts that may sign in through a GitHub provider, and AdminUsers and AdminOrganizations those that are instance admins
	AllowedUsers         []string
	AllowedOrganizations []string
	AdminUsers           []string
	AdminOrganizations   []string
}

type Dependencies struct {
	DB            *database.DB
	Workspaces    WorkspaceResolver
	Settings      UsageUnits
	EncryptionKey []byte
	Config        Config
}

type Module struct {
	service *Service
	handler *handler
}

func New(deps Dependencies) (*Module, error) {
	sessionKey, err := crypto.DeriveKey(deps.EncryptionKey, "session")
	if err != nil {
		return nil, fmt.Errorf("failed to derive session key: %w", err)
	}

	// Cookies are only marked Secure when the app is served over HTTPS, so plain-HTTP development still works
	codec := &cookieCodec{key: sessionKey, secure: strings.HasPrefix(deps.Config.AppURL, "https://")}
	service, err := newService(deps.DB, deps.Config, codec, deps.Workspaces)
	if err != nil {
		return nil, err
	}

	return &Module{service: service, handler: &handler{service: service, settings: deps.Settings}}, nil
}

// RegisterRoutes mounts the login flow and the current-user endpoint
// loginRateLimit protects the unauthenticated login endpoints
func (m *Module) RegisterRoutes(api huma.API, auth, loginRateLimit huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-login-providers", http.MethodGet, "/api/auth/providers", "Auth"), nil, m.handler.listProviders)
	httpserver.Register(api, httpserver.Operation("login", http.MethodGet, "/api/auth/login/{provider}", "Auth"), loginRateLimit, m.handler.login)
	httpserver.Register(api, httpserver.Operation("login-callback", http.MethodGet, "/api/auth/callback/{provider}", "Auth"), loginRateLimit, m.handler.callback)
	httpserver.Register(api, httpserver.Operation("logout", http.MethodPost, "/api/auth/logout", "Auth"), nil, m.handler.logout)
	httpserver.Register(api, httpserver.Operation("get-current-user", http.MethodGet, "/api/users/me", "Auth"), auth, m.handler.me)

	// Every user of the instance, for instance admins
	instanceAdmin := httpserver.Access{InstanceAdmin: true, SessionOnly: true}
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("list-users", http.MethodGet, "/api/admin/users", "Admin"), instanceAdmin), auth, m.handler.listUsers)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-user", http.MethodPatch, "/api/admin/users/{id}", "Admin"), instanceAdmin), auth, m.handler.updateUser)
}

// VerifySession is used by the auth middleware
func (m *Module) VerifySession(ctx context.Context, value string) (principal.Principal, error) {
	return m.service.VerifySession(ctx, value)
}

// PinGitHubNames ties the usernames and organizations that GitHub providers list to the numeric IDs holding them now
// It only logs what it can't look up, since those names are still tied at their first sign-in
func (m *Module) PinGitHubNames(ctx context.Context) {
	for _, p := range m.service.providers {
		if gh, ok := p.provider.(*githubProvider); ok {
			gh.pin(ctx)
		}
	}
}

// SessionCookie moves the caller's session into another workspace, used when a user switches workspaces
func (m *Module) SessionCookie(p principal.Principal, workspaceID string) (http.Cookie, error) {
	return m.service.SessionCookie(p, workspaceID)
}

// TestAccount is an account the e2e test endpoints sign in as without a sign-in provider
type TestAccount struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Admin         bool
}

// SignInForTest signs in as the account like a real login through the provider would, used by the e2e test endpoints
// It returns the user, the session cookie and where the login would redirect to
func (m *Module) SignInForTest(ctx context.Context, account TestAccount, providerID, redirect string) (string, http.Cookie, string, error) {
	cookie, redirect, err := m.service.signIn(ctx, identity{
		Issuer: account.Issuer, Subject: account.Subject, Email: account.Email, EmailVerified: account.EmailVerified, Name: account.Name, Admin: account.Admin,
	}, providerID, safeRedirect(redirect))
	if err != nil {
		return "", http.Cookie{}, "", err
	}
	claims, err := m.service.codec.parseSession(cookie.Value)
	if err != nil {
		return "", http.Cookie{}, "", err
	}
	return claims.UserID, cookie, redirect, nil
}
