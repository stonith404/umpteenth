package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// WorkspaceResolver picks the workspace a user signs into
type WorkspaceResolver interface {
	DefaultWorkspaceID(ctx context.Context) (string, error)
}

// Config holds the OIDC client settings
type Config struct {
	AppURL        string
	Issuer        string
	ClientID      string
	ClientSecret  string
	AllowedGroups []string
}

type Dependencies struct {
	DB            *database.DB
	Workspaces    WorkspaceResolver
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
	codec := &cookieCodec{key: sessionKey, secure: len(deps.Config.AppURL) > 5 && deps.Config.AppURL[:5] == "https"}
	service := newService(deps.DB, deps.Config, codec, deps.Workspaces)

	return &Module{service: service, handler: &handler{service: service}}, nil
}

// RegisterRoutes mounts the login flow and the current-user endpoint
// loginRateLimit protects the unauthenticated login endpoints
func (m *Module) RegisterRoutes(api huma.API, auth, loginRateLimit huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("login", http.MethodGet, "/api/auth/login", "Auth"), loginRateLimit, m.handler.login)
	httpserver.Register(api, httpserver.Operation("login-callback", http.MethodGet, "/api/auth/callback", "Auth"), loginRateLimit, m.handler.callback)
	httpserver.Register(api, httpserver.Operation("logout", http.MethodPost, "/api/auth/logout", "Auth"), nil, m.handler.logout)
	httpserver.Register(api, httpserver.Operation("get-current-user", http.MethodGet, "/api/users/me", "Auth"), auth, m.handler.me)
}

// VerifySession is used by the auth middleware
func (m *Module) VerifySession(ctx context.Context, value string) (principal.Principal, error) {
	return m.service.VerifySession(ctx, value)
}

// SessionCookieFor issues a session cookie for the user, used by the e2e test endpoints
func (m *Module) SessionCookieFor(ctx context.Context, userID string) (http.Cookie, error) {
	return m.service.SessionCookieFor(ctx, userID)
}

// UpsertUser creates or updates a user by OIDC subject, used by the e2e test endpoints
func (m *Module) UpsertUser(ctx context.Context, subject, email, name string) (string, error) {
	user, err := m.service.UpsertUser(ctx, subject, email, name)
	return user.ID, err
}
