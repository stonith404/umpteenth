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
	PasskeyAccount bool    `json:"passkeyAccount" doc:"Whether the account signs in with passkeys, which instance admins manage, instead of a sign-in provider"`
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
		d.PasskeyAccount = d.Issuer == passkeyIssuer
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
		Deactivated *bool `json:"deactivated,omitempty" doc:"A deactivated user can't sign in, and their sessions and API tokens stop working"`
		IsAdmin     *bool `json:"isAdmin,omitempty" doc:"Makes a passkey account an instance admin or takes that away, while sign-in providers decide on their own accounts"`
	}
}

func (h *handler) updateUser(ctx context.Context, in *updateUserInput) (*struct{}, error) {
	p, _ := principal.From(ctx)
	if in.Body.Deactivated != nil {
		err := h.setDeactivated(ctx, p, in.ID, *in.Body.Deactivated)
		if err != nil {
			return nil, err
		}
	}
	if in.Body.IsAdmin != nil {
		err := h.setAdmin(ctx, p, in.ID, *in.Body.IsAdmin)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (h *handler) setDeactivated(ctx context.Context, p principal.Principal, userID string, deactivated bool) error {
	if userID == p.UserID && deactivated {
		return apperror.Conflict("You can't deactivate yourself")
	}

	var disabledAt *int64
	if deactivated {
		disabledAt = new(database.Now())
	}
	n, err := h.service.queries.SetUserDisabled(ctx, authdb.SetUserDisabledParams{ID: userID, DisabledAt: disabledAt})
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	if n == 0 {
		return apperror.NotFound("User")
	}

	// Deactivating ends the user's sessions for good, so reactivating them later doesn't bring back a copied cookie
	if deactivated {
		err = h.service.queries.DeleteUserSessions(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to end the user's sessions: %w", err)
		}
	}
	return nil
}

func (h *handler) setAdmin(ctx context.Context, p principal.Principal, userID string, admin bool) error {
	if userID == p.UserID && !admin {
		return apperror.Conflict("You can't remove your own admin rights")
	}

	// A sign-in provider sets the admins among its accounts again at every sign-in, which would undo a change made here
	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if user.Issuer != passkeyIssuer {
		return apperror.Conflict(h.service.providerName(user.Issuer) + " decides whether this user is an instance admin, through its admin options")
	}
	_, err = h.service.queries.SetLocalUserAdmin(ctx, authdb.SetLocalUserAdminParams{ID: userID, Issuer: passkeyIssuer, IsAdmin: admin})
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	return nil
}

type signInLinkDto struct {
	URL       string `json:"url" doc:"Signs in the account once, so its owner can add a passkey"`
	ExpiresAt int64  `json:"expiresAt"`
}

type createUserInput struct {
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"100"`
		Email   string `json:"email,omitempty" maxLength:"254" format:"email" doc:"Counts as verified, so invites sent to it reach the account"`
		IsAdmin bool   `json:"isAdmin,omitempty" doc:"Makes the account an instance admin"`
	}
}

type createUserOutput struct {
	Body struct {
		User       adminUserDto  `json:"user"`
		SignInLink signInLinkDto `json:"signInLink"`
	}
}

func (h *handler) createUser(ctx context.Context, in *createUserInput) (*createUserOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, apperror.InvalidField("name", "required", "is required")
	}
	user, link, err := h.service.CreatePasskeyUser(ctx, name, strings.TrimSpace(in.Body.Email), in.Body.IsAdmin)
	if err != nil {
		return nil, err
	}
	out := &createUserOutput{}
	out.Body.User = adminUserDto{
		ID: user.ID, Name: user.Name, Email: user.Email, Issuer: user.Issuer, Provider: h.service.providerName(user.Issuer), PasskeyAccount: true,
		IsAdmin: user.IsAdmin, CreatedAt: user.CreatedAt,
	}
	out.Body.SignInLink = signInLinkDto(link)
	return out, nil
}

type userIDInput struct {
	ID string `path:"id"`
}

type signInLinkOutput struct {
	Body signInLinkDto
}

func (h *handler) createSignInLink(ctx context.Context, in *userIDInput) (*signInLinkOutput, error) {
	link, err := h.service.CreateSignInLink(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &signInLinkOutput{Body: signInLinkDto(link)}, nil
}

// providerName names the sign-in provider whose accounts carry the issuer, as the login page labels it
// A user whose provider was removed from the config gets the issuer's host, which still tells admins where the account comes from
func (s *Service) providerName(issuer string) string {
	if issuer == passkeyIssuer {
		return "Passkey"
	}

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
