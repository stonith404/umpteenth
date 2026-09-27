package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// oidcProvider signs in through any OpenID Connect identity provider, which it finds through the issuer's discovery document
type oidcProvider struct {
	cfg ProviderConfig

	// The discovered provider is cached after the first successful discovery, which is safe because it is immutable configuration
	mu         sync.Mutex
	discovered *oidc.Provider
}

// discover fetches the provider's metadata lazily, so the app starts even when an identity provider is briefly unreachable
func (p *oidcProvider) discover(ctx context.Context) (*oidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovered == nil {
		discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		discovered, err := oidc.NewProvider(discoverCtx, p.cfg.Issuer)
		if err != nil {
			return nil, apperror.Unavailable(err, "Identity provider is unreachable")
		}
		p.discovered = discovered
	}
	return p.discovered, nil
}

func (p *oidcProvider) oauthConfig(discovered *oidc.Provider, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     discovered.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
}

// issuer is the configured issuer URL, which the ID tokens of the provider must name
func (p *oidcProvider) issuer() string {
	return p.cfg.Issuer
}

func (p *oidcProvider) authCodeURL(ctx context.Context, redirectURL string, state loginState) (string, error) {
	discovered, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	return p.oauthConfig(discovered, redirectURL).AuthCodeURL(state.State, oidc.Nonce(state.Nonce), oauth2.S256ChallengeOption(state.Verifier)), nil
}

func (p *oidcProvider) identify(ctx context.Context, redirectURL, code string, state loginState) (identity, error) {
	discovered, err := p.discover(ctx)
	if err != nil {
		return identity{}, err
	}

	// Exchange the code with the PKCE verifier and verify the ID token
	token, err := p.oauthConfig(discovered, redirectURL).Exchange(ctx, code, oauth2.VerifierOption(state.Verifier))
	if err != nil {
		return identity{}, apperror.LoginFailed(fmt.Errorf("code exchange failed: %w", err))
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return identity{}, apperror.LoginFailed(errors.New("token response has no id_token"))
	}
	idToken, err := discovered.Verifier(&oidc.Config{ClientID: p.cfg.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return identity{}, apperror.LoginFailed(fmt.Errorf("invalid ID token: %w", err))
	}
	if idToken.Nonce != state.Nonce {
		return identity{}, apperror.LoginFailed(errors.New("nonce mismatch"))
	}

	var claims struct {
		Email             string   `json:"email"`
		EmailVerified     flexBool `json:"email_verified"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Picture           string   `json:"picture"`
		Groups            []string `json:"groups"`
	}
	err = idToken.Claims(&claims)
	if err != nil {
		return identity{}, apperror.LoginFailed(fmt.Errorf("invalid claims: %w", err))
	}

	// The identity provider decides who may use the client, allowed_groups optionally narrows it further, and admins always get in
	inGroups := func(groups []string) bool {
		return slices.ContainsFunc(claims.Groups, func(g string) bool { return slices.Contains(groups, g) })
	}
	admin := inGroups(p.cfg.AdminGroups)
	if len(p.cfg.AllowedGroups) > 0 && !admin && !inGroups(p.cfg.AllowedGroups) {
		return identity{}, apperror.Forbidden("Your account is not in an allowed group")
	}

	// An address only counts as verified when the identity provider says so, since some let people enter any address
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	return identity{
		Issuer:        idToken.Issuer,
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.Email != "" && bool(claims.EmailVerified),
		Name:          name,
		Picture:       claims.Picture,
		Admin:         admin,
	}, nil
}

// flexBool reads a JSON boolean, or the string form some identity providers send instead
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = flexBool(strings.EqualFold(v, "true"))
	default:
		*b = false
	}
	return nil
}
