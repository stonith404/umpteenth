package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// adminUserDto is a user as instance admins see them
type adminUserDto struct {
	ID             string  `json:"id"`
	Name           *string `json:"name"`
	Email          *string `json:"email"`
	Picture        *string `json:"picture"`
	Issuer         string  `json:"issuer" doc:"The issuer URL of the sign-in provider the account belongs to"`
	Provider       string  `json:"provider" doc:"The name of the sign-in provider the account belongs to, or the issuer's host when no configured provider has its issuer"`
	IsAdmin        bool    `json:"isAdmin"`
	Deactivated    bool    `json:"deactivated"`
	WorkspaceCount int64   `json:"workspaceCount"`
	CreatedAt      int64   `json:"createdAt"`
	LastLoginAt    *int64  `json:"lastLoginAt"`
}

type listUsersInput struct {
	httpserver.ListParams
}

var usersSpec = &listquery.Spec{
	Select:        "SELECT u.id, u.name, u.email, u.picture, u.issuer, u.is_admin, u.disabled_at, (SELECT COUNT(*) FROM workspace_members m WHERE m.user_id = u.id), u.created_at, u.last_login_at FROM users u",
	From:          "FROM users u",
	Sorts:         map[string]string{"name": "LOWER(COALESCE(u.name, u.email, ''))", "createdAt": "u.created_at", "lastLoginAt": "u.last_login_at"},
	NullableSorts: []string{"lastLoginAt"},
	DefaultSort:   "name",
	Search:        []string{"u.name", "u.email"},
	TieBreaker:    "u.id",
}

func (h *handler) listUsers(ctx context.Context, in *listUsersInput) (*httpserver.PaginatedOutput[adminUserDto], error) {
	items, total, err := listquery.Run(ctx, h.service.db, listquery.New(usersSpec), in.ToQuery(), func(rows *sql.Rows) (adminUserDto, error) {
		var d adminUserDto
		var disabledAt *int64
		err := rows.Scan(&d.ID, &d.Name, &d.Email, &d.Picture, &d.Issuer, &d.IsAdmin, &disabledAt, &d.WorkspaceCount, &d.CreatedAt, &d.LastLoginAt)
		d.Deactivated = disabledAt != nil
		d.Provider = h.service.providerName(d.Issuer)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type updateUserInput struct {
	ID   string `path:"id"`
	Body struct {
		Deactivated bool `json:"deactivated" doc:"A deactivated user can't sign in, and their sessions and API tokens stop working"`
	}
}

func (h *handler) updateUser(ctx context.Context, in *updateUserInput) (*struct{}, error) {
	p, _ := principal.From(ctx)
	if in.ID == p.UserID && in.Body.Deactivated {
		return nil, apperror.Conflict("You can't deactivate yourself")
	}

	var disabledAt *int64
	if in.Body.Deactivated {
		disabledAt = new(database.Now())
	}
	n, err := h.service.queries.SetUserDisabled(ctx, authdb.SetUserDisabledParams{ID: in.ID, DisabledAt: disabledAt})
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("User")
	}

	// Deactivating ends the user's sessions for good, so reactivating them later doesn't bring back a copied cookie
	if in.Body.Deactivated {
		err = h.service.queries.DeleteUserSessions(ctx, in.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to end the user's sessions: %w", err)
		}
	}
	return nil, nil
}

// providerName names the sign-in provider whose accounts carry the issuer, as the login page labels it
// A user whose provider was removed from the config gets the issuer's host, which still tells admins where the account comes from
func (s *Service) providerName(issuer string) string {
	// Issuer URLs are compared without a trailing slash, which some identity providers add and others leave out
	want := strings.TrimSuffix(issuer, "/")
	for _, p := range s.providers {
		if strings.TrimSuffix(p.issuer(), "/") == want {
			return p.Name
		}
	}
	if u, err := url.Parse(issuer); err == nil && u.Host != "" {
		return u.Host
	}
	return issuer
}
