package auth

import (
	"context"
	"net/http"
	"net/url"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

type handler struct {
	service  *Service
	settings UsageUnits
}

type loginProviderDto struct {
	ID      string `json:"id"`
	Type    string `json:"type" enum:"oidc,github" doc:"How the provider signs in, which picks the default icon"`
	Name    string `json:"name"`
	Icon    string `json:"icon,omitempty" doc:"Image URL for the sign-in button, an http(s) URL or a data:image URI"`
	Primary bool   `json:"primary" doc:"Whether the provider gets the large sign-in button"`
}

type listProvidersOutput struct {
	Body []loginProviderDto
}

func (h *handler) listProviders(_ context.Context, _ *struct{}) (*listProvidersOutput, error) {
	providers := h.service.Providers()
	out := &listProvidersOutput{Body: make([]loginProviderDto, 0, len(providers))}
	for _, p := range providers {
		out.Body = append(out.Body, loginProviderDto{ID: p.ID, Type: p.Type, Name: p.Name, Icon: p.Icon, Primary: p.Primary})
	}
	return out, nil
}

type loginInput struct {
	Provider string `path:"provider" doc:"ID of the sign-in provider"`
	Redirect string `query:"redirect" doc:"Relative path to return to after login"`
}

type redirectOutput struct {
	Status    int
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func (h *handler) login(ctx context.Context, in *loginInput) (*redirectOutput, error) {
	authURL, cookie, err := h.service.BeginLogin(ctx, in.Provider, in.Redirect)
	if appErr, ok := apperror.As(err); ok {
		// The login button is a full-page navigation, so errors go back to the login page
		return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + string(appErr.Code())}, nil
	} else if err != nil {
		return nil, err
	}
	return &redirectOutput{Status: http.StatusFound, Location: authURL, SetCookie: []http.Cookie{cookie}}, nil
}

type callbackInput struct {
	Provider    string      `path:"provider" doc:"ID of the sign-in provider the login started with"`
	Code        string      `query:"code"`
	State       string      `query:"state"`
	Error       string      `query:"error"`
	LoginCookie http.Cookie `cookie:"umpteenth_login"`
}

func (h *handler) callback(ctx context.Context, in *callbackInput) (*redirectOutput, error) {
	// The provider reports user-facing failures such as access_denied as a query parameter
	if in.Error != "" {
		return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + url.QueryEscape(in.Error)}, nil
	}

	// Like the login, the callback is a full-page navigation, so errors go back to the login page, including a provider API that is down
	cookie, redirect, err := h.service.FinishLogin(ctx, in.Provider, in.LoginCookie.Value, in.Code, in.State)
	if appErr, ok := apperror.As(err); ok {
		return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + string(appErr.Code())}, nil
	} else if err != nil {
		return nil, err
	}

	clearLogin := h.service.codec.expired(loginCookieName, "/api/auth")
	return &redirectOutput{Status: http.StatusFound, Location: redirect, SetCookie: []http.Cookie{cookie, clearLogin}}, nil
}

type logoutInput struct {
	Session http.Cookie `cookie:"umpteenth_session"`
}

type logoutOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func (h *handler) logout(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
	err := h.service.Logout(ctx, in.Session.Value)
	if err != nil {
		return nil, err
	}
	return &logoutOutput{SetCookie: h.service.LogoutCookies()}, nil
}

// sessionWorkspaceDto is the workspace a session is in
type sessionWorkspaceDto struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Role principal.Role `json:"role" enum:"owner,admin,member" doc:"The caller's role in the workspace, which is their creator's for an API token"`
	// Every page shows usage, so the unit comes with the session instead of needing the settings
	UsageUnit string `json:"usageUnit" enum:"price,tokens" doc:"Whether the UI shows usage as a price in US dollars or as input and output tokens, the workspace's usageUnit setting"`
}

type userDto struct {
	ID          string  `json:"id"`
	Email       *string `json:"email"`
	Name        *string `json:"name"`
	Picture     *string `json:"picture" doc:"Profile picture URL from the sign-in provider"`
	WorkspaceID string  `json:"workspaceId"`
	ViaToken    bool    `json:"viaToken"`
	// The login page remembers the provider as the last one used in this browser
	LoginProvider     string              `json:"loginProvider,omitempty" doc:"ID of the sign-in provider the session signed in with"`
	IsAdmin           bool                `json:"isAdmin" doc:"Whether the user is an instance admin, who manages every user and workspace"`
	WorkspacesEnabled bool                `json:"workspacesEnabled" doc:"Whether people can have several workspaces on this instance"`
	Workspace         sessionWorkspaceDto `json:"workspace"`
}

type meOutput struct {
	Body userDto
}

func (h *handler) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	p, ok := principal.From(ctx)
	if !ok {
		return nil, apperror.NotSignedIn()
	}
	ws, err := h.service.workspaces.Get(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	unit, err := h.settings.UsageUnit(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	workspace := sessionWorkspaceDto{ID: ws.ID, Name: ws.Name, Role: p.Role, UsageUnit: unit}
	enabled := h.service.workspaces.Enabled()

	// API tokens act on behalf of the workspace, not a person
	if p.UserID == "" {
		return &meOutput{Body: userDto{ID: p.TokenID, WorkspaceID: p.WorkspaceID, ViaToken: true, WorkspacesEnabled: enabled, Workspace: workspace}}, nil
	}

	user, err := h.service.GetUser(ctx, p.UserID)
	if apperror.IsCode(err, apperror.CodeNotFound) {
		// The user was deleted after the session was issued, which counts as signed out
		return nil, apperror.NotSignedIn()
	} else if err != nil {
		return nil, err
	}
	return &meOutput{Body: userDto{
		ID:                user.ID,
		Email:             user.Email,
		Name:              user.Name,
		Picture:           user.Picture,
		WorkspaceID:       p.WorkspaceID,
		LoginProvider:     p.LoginProvider,
		IsAdmin:           p.InstanceAdmin,
		WorkspacesEnabled: enabled,
		Workspace:         workspace,
	}}, nil
}
