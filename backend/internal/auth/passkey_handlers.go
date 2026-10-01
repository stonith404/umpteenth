package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

type setupOutput struct {
	Body struct {
		Open bool `json:"open" doc:"Whether the instance has no users yet, so whoever opens it creates its first account, an instance admin, with a passkey"`
	}
}

func (h *handler) getSetup(ctx context.Context, _ *struct{}) (*setupOutput, error) {
	open, err := h.service.SetupOpen(ctx)
	if err != nil {
		return nil, err
	}
	out := &setupOutput{}
	out.Body.Open = open
	return out, nil
}

// passkeyOptionsOutput hands the browser the options of a passkey ceremony, with the cookie that names the ceremony
type passkeyOptionsOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Options json.RawMessage `json:"options" doc:"The publicKey options for navigator.credentials, in their JSON form"`
	}
}

func optionsOutput(options json.RawMessage, cookie http.Cookie) *passkeyOptionsOutput {
	out := &passkeyOptionsOutput{SetCookie: []http.Cookie{cookie}}
	out.Body.Options = options
	return out
}

// passkeyResponseInput is the browser's answer to a passkey ceremony
type passkeyResponseInput struct {
	Ceremony http.Cookie `cookie:"umpteenth_passkey"`
	Body     struct {
		Credential json.RawMessage `json:"credential" doc:"The PublicKeyCredential the browser created or signed with, in its JSON form"`
	}
}

// signedInOutput starts a session and tells the page where to go, since passkey sign-ins happen in the page instead of through redirects
type signedInOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Redirect string `json:"redirect" doc:"Relative path to continue at"`
	}
}

func (h *handler) signedIn(session http.Cookie, redirect string) *signedInOutput {
	out := &signedInOutput{SetCookie: []http.Cookie{session, h.service.ClearPasskeyCookie()}}
	out.Body.Redirect = redirect
	return out
}

type beginPasskeySignInInput struct {
	Body struct {
		Redirect string `json:"redirect,omitempty" doc:"Relative path to return to after signing in"`
	}
}

func (h *handler) beginPasskeySignIn(ctx context.Context, in *beginPasskeySignInInput) (*passkeyOptionsOutput, error) {
	options, cookie, err := h.service.BeginPasskeySignIn(ctx, in.Body.Redirect)
	if err != nil {
		return nil, err
	}
	return optionsOutput(options, cookie), nil
}

func (h *handler) passkeySignIn(ctx context.Context, in *passkeyResponseInput) (*signedInOutput, error) {
	session, redirect, err := h.service.FinishPasskeySignIn(ctx, in.Ceremony.Value, in.Body.Credential)
	if err != nil {
		return nil, err
	}
	return h.signedIn(session, redirect), nil
}

type beginPasskeySignUpInput struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"100"`
		Email    string `json:"email,omitempty" maxLength:"254" format:"email"`
		Redirect string `json:"redirect,omitempty" doc:"Relative path to continue at, which is the invite page for a sign-up through an invite link"`
	}
}

func (h *handler) beginPasskeySignUp(ctx context.Context, in *beginPasskeySignUpInput) (*passkeyOptionsOutput, error) {
	options, cookie, err := h.service.BeginPasskeySignUp(ctx, SignUp{
		Name: strings.TrimSpace(in.Body.Name), Email: strings.TrimSpace(in.Body.Email), Redirect: in.Body.Redirect,
	})
	if err != nil {
		return nil, err
	}
	return optionsOutput(options, cookie), nil
}

func (h *handler) passkeySignUp(ctx context.Context, in *passkeyResponseInput) (*signedInOutput, error) {
	session, redirect, err := h.service.FinishPasskeySignUp(ctx, in.Ceremony.Value, in.Body.Credential)
	if err != nil {
		return nil, err
	}
	return h.signedIn(session, redirect), nil
}

type useSignInLinkInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" doc:"The token at the end of the sign-in link"`
	}
}

func (h *handler) useSignInLink(ctx context.Context, in *useSignInLinkInput) (*signedInOutput, error) {
	session, redirect, err := h.service.UseSignInLink(ctx, in.Body.Token)
	if err != nil {
		return nil, err
	}
	return h.signedIn(session, redirect), nil
}

type passkeyDto struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
}

func toPasskeyDto(p authdb.Passkey) passkeyDto {
	return passkeyDto{ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
}

type listPasskeysOutput struct {
	Body []passkeyDto
}

func (h *handler) listPasskeys(ctx context.Context, _ *struct{}) (*listPasskeysOutput, error) {
	p, _ := principal.From(ctx)
	passkeys, err := h.service.ListPasskeys(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := &listPasskeysOutput{Body: make([]passkeyDto, 0, len(passkeys))}
	for _, passkey := range passkeys {
		out.Body = append(out.Body, toPasskeyDto(passkey))
	}
	return out, nil
}

func (h *handler) beginAddPasskey(ctx context.Context, _ *struct{}) (*passkeyOptionsOutput, error) {
	p, _ := principal.From(ctx)
	options, cookie, err := h.service.BeginAddPasskey(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return optionsOutput(options, cookie), nil
}

type passkeyOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      passkeyDto
}

func (h *handler) addPasskey(ctx context.Context, in *passkeyResponseInput) (*passkeyOutput, error) {
	p, _ := principal.From(ctx)
	passkey, err := h.service.FinishAddPasskey(ctx, p.UserID, in.Ceremony.Value, in.Body.Credential)
	if err != nil {
		return nil, err
	}
	return &passkeyOutput{SetCookie: []http.Cookie{h.service.ClearPasskeyCookie()}, Body: toPasskeyDto(passkey)}, nil
}

type renamePasskeyInput struct {
	ID   string `path:"id"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
	}
}

func (h *handler) renamePasskey(ctx context.Context, in *renamePasskeyInput) (*struct{}, error) {
	p, _ := principal.From(ctx)
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, apperror.InvalidField("name", "required", "is required")
	}
	return nil, h.service.RenamePasskey(ctx, p.UserID, in.ID, name)
}

type passkeyIDInput struct {
	ID string `path:"id"`
}

func (h *handler) deletePasskey(ctx context.Context, in *passkeyIDInput) (*struct{}, error) {
	p, _ := principal.From(ctx)
	return nil, h.service.DeletePasskey(ctx, p.UserID, in.ID)
}

type updateProfileInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"100"`
		Email string `json:"email,omitempty" maxLength:"254" format:"email"`
	}
}

func (h *handler) updateProfile(ctx context.Context, in *updateProfileInput) (*struct{}, error) {
	p, _ := principal.From(ctx)
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, apperror.InvalidField("name", "required", "is required")
	}
	_, err := h.service.UpdateProfile(ctx, p.UserID, name, strings.TrimSpace(in.Body.Email))
	return nil, err
}
