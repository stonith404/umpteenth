// Package apitokens manages workspace-scoped API tokens for automation
package apitokens

import (
	"cmp"
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

// Roles resolves the role a token acts with, which is its creator's membership role in the token's workspace
type Roles interface {
	TokenAccess(ctx context.Context, workspaceID, creatorID string) (principal.Role, error)
}

type Dependencies struct {
	DB    *database.DB
	Roles Roles
}

type Module struct {
	db      *database.DB
	queries *apitokensdb.Queries
	roles   Roles
}

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, queries: apitokensdb.New(deps.DB), roles: deps.Roles}
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	// A token that could mint tokens would make expiry and revocation meaningless, since a leaked one could outlive itself
	session := httpserver.Access{SessionOnly: true}
	httpserver.Register(api, httpserver.Operation("list-api-tokens", http.MethodGet, "/api/tokens", "API tokens"), auth, m.list)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("create-api-token", http.MethodPost, "/api/tokens", "API tokens"), session), auth, m.create)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-api-token", http.MethodDelete, "/api/tokens/{id}", "API tokens"), session), auth, m.delete)
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

	// The token acts with its creator's role, so it stops working once the creator loses access, which is checked before usage is recorded
	role, err := m.creatorRole(ctx, row.WorkspaceID, row.CreatedBy)
	if err != nil {
		return principal.Principal{}, err
	}

	// Only record usage once a minute, so busy automation doesn't turn every request into a write
	if row.LastUsedAt == nil || now-*row.LastUsedAt > time.Minute.Milliseconds() {
		_ = m.queries.TouchAPIToken(ctx, apitokensdb.TouchAPITokenParams{ID: row.ID, LastUsedAt: &now})
	}

	return principal.Principal{WorkspaceID: row.WorkspaceID, TokenID: row.ID, TokenCreatorID: *row.CreatedBy, Role: role}, nil
}

// ValidateAPITokenID confirms that an already authenticated token still exists, has not expired, and its creator still has access
func (m *Module) ValidateAPITokenID(ctx context.Context, workspaceID, tokenID string) error {
	var expiresAt *int64
	var createdBy *string
	err := m.db.QueryRowContext(ctx, "SELECT expires_at, created_by FROM api_tokens WHERE workspace_id = $1 AND id = $2", workspaceID, tokenID).Scan(&expiresAt, &createdBy)
	if database.IsNotFound(err) {
		return apperror.InvalidToken()
	} else if err != nil {
		return fmt.Errorf("failed to revalidate API token: %w", err)
	}
	if expiresAt != nil && *expiresAt <= database.Now() {
		return apperror.InvalidToken()
	}
	_, err = m.creatorRole(ctx, workspaceID, createdBy)
	return err
}

// creatorRole is the role a token acts with, and a token whose creator is gone or lost access is invalid
func (m *Module) creatorRole(ctx context.Context, workspaceID string, createdBy *string) (principal.Role, error) {
	if createdBy == nil {
		return "", apperror.InvalidToken()
	}
	return m.roles.TokenAccess(ctx, workspaceID, *createdBy)
}

type apiTokenDto struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CreatedAt  int64   `json:"createdAt"`
	LastUsedAt *int64  `json:"lastUsedAt"`
	ExpiresAt  *int64  `json:"expiresAt"`
	CreatedBy  *string `json:"createdBy" doc:"Name or email address of the user whose role the token acts with"`
}

type listInput struct {
	httpserver.ListParams
}

var listSpec = &listquery.Spec{
	Select:        "SELECT t.id, t.name, t.created_at, t.last_used_at, t.expires_at, COALESCE(u.name, u.email) FROM api_tokens t LEFT JOIN users u ON u.id = t.created_by",
	From:          "FROM api_tokens t",
	Sorts:         map[string]string{"name": "t.name", "createdAt": "t.created_at", "lastUsedAt": "t.last_used_at", "expiresAt": "t.expires_at"},
	NullableSorts: []string{"lastUsedAt", "expiresAt"},
	DefaultSort:   "-createdAt",
	Search:        []string{"t.name"},
	TieBreaker:    "t.id",
}

// canManageAll reports whether the caller may see and revoke every token of the workspace, instead of only their own
func canManageAll(ctx context.Context) bool {
	return principal.RoleOf(ctx).AtLeast(principal.RoleAdmin)
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[apiTokenDto], error) {
	q := listquery.New(listSpec).WhereEq("t.workspace_id", principal.WorkspaceID(ctx))
	if !canManageAll(ctx) {
		p, _ := principal.From(ctx)
		q.WhereEq("t.created_by", cmp.Or(p.UserID, p.TokenCreatorID))
	}
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (apiTokenDto, error) {
		var d apiTokenDto
		err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastUsedAt, &d.ExpiresAt, &d.CreatedBy)
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

func (m *Module) create(ctx context.Context, in *createInput) (*createOutput, error) {
	// A token acts with its creator's membership role, which an instance admin who only opened the workspace doesn't have
	_, err := m.roles.TokenAccess(ctx, principal.WorkspaceID(ctx), principal.UserIDOf(ctx))
	if apperror.IsCode(err, apperror.CodeInvalidToken) {
		return nil, apperror.Forbidden("Only members of the workspace can create API tokens in it")
	} else if err != nil {
		return nil, err
	}

	// The length check runs before trimming, so a name made only of spaces has to be caught here
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, apperror.InvalidField("name", "required", "must not be empty")
	}
	now := database.Now()
	if in.Body.ExpiresAt != nil && *in.Body.ExpiresAt <= now {
		return nil, apperror.InvalidField("expiresAt", "invalid", "must be in the future")
	}

	token := TokenPrefix + crypto.RandomToken(32)
	id := database.NewID()
	err = m.queries.CreateAPIToken(ctx, apitokensdb.CreateAPITokenParams{
		ID:          id,
		WorkspaceID: principal.WorkspaceID(ctx),
		Name:        name,
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
	out.Body.APIToken = apiTokenDto{ID: id, Name: name, CreatedAt: now, ExpiresAt: in.Body.ExpiresAt}
	return out, nil
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	// Members revoke their own tokens, and admins anyone's
	var createdBy *string
	if !canManageAll(ctx) {
		createdBy = principal.UserIDPtr(ctx)
	}
	n, err := m.queries.DeleteAPIToken(ctx, apitokensdb.DeleteAPITokenParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID, CreatedBy: createdBy})
	if err != nil {
		return nil, fmt.Errorf("failed to delete API token: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("API token")
	}
	return nil, nil
}
