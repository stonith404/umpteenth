package auth

import (
	"context"
	"net/http"
	"net/url"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

type handler struct {
	service *Service
}

type loginInput struct {
	Redirect string `query:"redirect" doc:"Relative path to return to after login"`
}

type redirectOutput struct {
	Status    int
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func (h *handler) login(ctx context.Context, in *loginInput) (*redirectOutput, error) {
	authURL, cookie, err := h.service.BeginLogin(ctx, in.Redirect)
	if appErr, ok := apperror.As(err); ok {
		// The login button is a full-page navigation, so errors go back to the login page
		return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + string(appErr.Code())}, nil
	} else if err != nil {
		return nil, err
	}
	return &redirectOutput{Status: http.StatusFound, Location: authURL, SetCookie: []http.Cookie{cookie}}, nil
}

type callbackInput struct {
	Code        string      `query:"code"`
	State       string      `query:"state"`
	Error       string      `query:"error"`
	LoginCookie http.Cookie `cookie:"umpteenth_login"`
}

func (h *handler) callback(ctx context.Context, in *callbackInput) (*redirectOutput, error) {
	// The identity provider reports user-facing failures such as access_denied as a query parameter
	if in.Error != "" {
		return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + url.QueryEscape(in.Error)}, nil
	}

	cookie, redirect, err := h.service.FinishLogin(ctx, in.LoginCookie.Value, in.Code, in.State)
	if err != nil {
		if appErr, ok := apperror.As(err); ok && appErr.GetStatus() < 500 {
			return &redirectOutput{Status: http.StatusFound, Location: "/login?error=" + string(appErr.Code())}, nil
		}
		return nil, err
	}

	clearLogin := h.service.codec.expired(loginCookieName, "/api/auth")
	return &redirectOutput{Status: http.StatusFound, Location: redirect, SetCookie: []http.Cookie{cookie, clearLogin}}, nil
}

type logoutOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func (h *handler) logout(_ context.Context, _ *struct{}) (*logoutOutput, error) {
	return &logoutOutput{SetCookie: h.service.LogoutCookies()}, nil
}

type userDto struct {
	ID          string  `json:"id"`
	Email       *string `json:"email"`
	Name        *string `json:"name"`
	WorkspaceID string  `json:"workspaceId"`
	ViaToken    bool    `json:"viaToken"`
}

type meOutput struct {
	Body userDto
}

func (h *handler) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	p, ok := principal.From(ctx)
	if !ok {
		return nil, apperror.NotSignedIn()
	}

	// API tokens act on behalf of the workspace, not a person
	if p.UserID == "" {
		return &meOutput{Body: userDto{ID: p.TokenID, WorkspaceID: p.WorkspaceID, ViaToken: true}}, nil
	}

	user, err := h.service.GetUser(ctx, p.UserID)
	if apperror.IsCode(err, apperror.CodeNotFound) {
		// The user was deleted after the session was issued, which counts as signed out
		return nil, apperror.NotSignedIn()
	} else if err != nil {
		return nil, err
	}
	return &meOutput{Body: userDto{ID: user.ID, Email: user.Email, Name: user.Name, WorkspaceID: p.WorkspaceID}}, nil
}
