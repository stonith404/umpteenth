// Package apitokens manages workspace-scoped API tokens for automation
package apitokens

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apitokens/apitokensdb"
	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// TokenPrefix makes Umpteenth tokens recognizable, e.g. for secret scanners
const TokenPrefix = "ump_"

type Dependencies struct {
	DB *database.DB
}

type Module struct {
	db      *database.DB
	queries *apitokensdb.Queries
}

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, queries: apitokensdb.New(deps.DB)}
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-api-tokens", http.MethodGet, "/api/tokens", "API tokens"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("create-api-token", http.MethodPost, "/api/tokens", "API tokens"), auth, m.create)
	httpserver.Register(api, httpserver.Operation("delete-api-token", http.MethodDelete, "/api/tokens/{id}", "API tokens"), auth, m.delete)
}

// ValidateAPIToken resolves a raw bearer token to its workspace, used by the auth middleware
func (m *Module) ValidateAPIToken(ctx context.Context, token string) (principal.Principal, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return principal.Principal{}, apperror.InvalidToken()
	}

	row, err := m.queries.GetAPITokenByHash(ctx, crypto.HashToken(token))
	if database.IsNotFound(err) {
		return principal.Principal{}, apperror.InvalidToken()
	} else if err != nil {
		return principal.Principal{}, fmt.Errorf("failed to load API token: %w", err)
	}

	now := database.Now()
	if row.ExpiresAt != nil && *row.ExpiresAt <= now {
		return principal.Principal{}, apperror.InvalidToken()
	}

	// Only record usage once a minute, so busy automation doesn't turn every request into a write
	if row.LastUsedAt == nil || now-*row.LastUsedAt > time.Minute.Milliseconds() {
		_ = m.queries.TouchAPIToken(ctx, apitokensdb.TouchAPITokenParams{ID: row.ID, LastUsedAt: &now})
	}

	return principal.Principal{WorkspaceID: row.WorkspaceID, TokenID: row.ID}, nil
}

type apiTokenDto struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
	ExpiresAt  *int64 `json:"expiresAt"`
}

type listInput struct {
	httpserver.ListParams
}

var listSpec = &listquery.Spec{
	Select:        "SELECT id, name, created_at, last_used_at, expires_at FROM api_tokens",
	From:          "FROM api_tokens",
	Sorts:         map[string]string{"name": "name", "createdAt": "created_at", "lastUsedAt": "last_used_at", "expiresAt": "expires_at"},
	NullableSorts: []string{"lastUsedAt", "expiresAt"},
	DefaultSort:   "-createdAt",
	Search:        []string{"name"},
	TieBreaker:    "id",
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[apiTokenDto], error) {
	q := listquery.New(listSpec).WhereEq("workspace_id", principal.WorkspaceID(ctx))
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (apiTokenDto, error) {
		var d apiTokenDto
		err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastUsedAt, &d.ExpiresAt)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type createInput struct {
	Body struct {
		Name      string `json:"name" minLength:"1" maxLength:"100"`
		ExpiresAt *int64 `json:"expiresAt,omitempty" doc:"Unix milliseconds; omit for a token that never expires"`
	}
}

type createOutput struct {
	Body struct {
		Token    string      `json:"token" doc:"Shown once, store it now"`
		APIToken apiTokenDto `json:"apiToken"`
	}
}

// requireSession keeps token management to signed-in users
// A token that could mint tokens would make expiry and revocation meaningless, since a leaked one could outlive itself
func requireSession(ctx context.Context) error {
	if principal.UserIDPtr(ctx) == nil {
		return apperror.Forbidden("API tokens can only be managed from a signed-in session")
	}
	return nil
}

func (m *Module) create(ctx context.Context, in *createInput) (*createOutput, error) {
	if err := requireSession(ctx); err != nil {
		return nil, err
	}

	// The length check runs before trimming, so a name made only of spaces has to be caught here
	if strings.TrimSpace(in.Body.Name) == "" {
		return nil, apperror.InvalidField("name", "required", "must not be empty")
	}
	now := database.Now()
	if in.Body.ExpiresAt != nil && *in.Body.ExpiresAt <= now {
		return nil, apperror.InvalidField("expiresAt", "invalid", "must be in the future")
	}

	token := TokenPrefix + crypto.RandomToken(32)
	id := database.NewID()
	err := m.queries.CreateAPIToken(ctx, apitokensdb.CreateAPITokenParams{
		ID:          id,
		WorkspaceID: principal.WorkspaceID(ctx),
		Name:        strings.TrimSpace(in.Body.Name),
		TokenHash:   crypto.HashToken(token),
		CreatedBy:   principal.UserIDPtr(ctx),
		CreatedAt:   now,
		ExpiresAt:   in.Body.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API token: %w", err)
	}

	out := &createOutput{}
	out.Body.Token = token
	out.Body.APIToken = apiTokenDto{ID: id, Name: strings.TrimSpace(in.Body.Name), CreatedAt: now, ExpiresAt: in.Body.ExpiresAt}
	return out, nil
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	if err := requireSession(ctx); err != nil {
		return nil, err
	}
	n, err := m.queries.DeleteAPIToken(ctx, apitokensdb.DeleteAPITokenParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to delete API token: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("API token")
	}
	return nil, nil
}
