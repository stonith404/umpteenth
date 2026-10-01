package auth

import (
	"context"
	"fmt"

	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
)

// Sign-in provider types, each implemented by one provider, and passkeys, which the login page lists like one
const (
	TypeOIDC    = "oidc"
	TypeGitHub  = "github"
	TypePasskey = "passkey"
)

// provider is one way to sign in, such as an OpenID Connect identity provider or GitHub
// Every type runs the OAuth authorization code flow with PKCE and differs in how it learns who signed in and whether they may
type provider interface {
	// authCodeURL returns the URL that sends the browser to the provider to sign in
	authCodeURL(ctx context.Context, redirectURL string, state loginState) (string, error)
	// identify redeems the authorization code and returns the account that signed in, or a Forbidden error when it may not use Umpteenth
	identify(ctx context.Context, redirectURL, code string, state loginState) (identity, error)
	// issuer is the Issuer of every identity the provider returns, which ties stored users back to the provider they sign in with
	issuer() string
}

// identity is the account a login ended with
type identity struct {
	// Issuer and Subject identify the account, since a subject is only unique at its issuer
	Issuer  string
	Subject string
	Email   string
	Name    string
	Picture string
	// EmailVerified is set when the provider vouched for the email address, which only then matches workspace invites
	EmailVerified bool
	// Admin is set when the provider's admin options name the account, which makes the user an instance admin
	Admin bool
}

// newProvider creates the implementation of the configured type
func newProvider(cfg ProviderConfig, queries *authdb.Queries) (provider, error) {
	switch cfg.Type {
	case TypeOIDC:
		return &oidcProvider{cfg: cfg}, nil
	case TypeGitHub:
		return newGitHubProvider(cfg, queries), nil
	default:
		return nil, fmt.Errorf("unknown type %q of sign-in provider %q", cfg.Type, cfg.ID)
	}
}
