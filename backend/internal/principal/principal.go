// Package principal carries the authenticated caller through a request context
package principal

import "context"

// Role is what a caller may do in a workspace
type Role string

// Workspace roles, each allowed everything the ones after it are
const (
	// RoleOwner is the one member who can delete the workspace or hand it over
	RoleOwner Role = "owner"
	// RoleAdmin manages members, invites, providers and the workspace settings
	RoleAdmin Role = "admin"
	// RoleMember works with jobs, runs, MCP servers, secrets and their own API tokens
	RoleMember Role = "member"
)

var roleRanks = map[Role]int{RoleMember: 1, RoleAdmin: 2, RoleOwner: 3}

// Valid reports whether the role is one of the known roles
func (r Role) Valid() bool {
	return roleRanks[r] > 0
}

// AtLeast reports whether the role is allowed everything minimum is
func (r Role) AtLeast(minimum Role) bool {
	return roleRanks[r] > 0 && roleRanks[r] >= roleRanks[minimum]
}

// Credential is how a caller authenticated
type Credential string

const (
	// CredentialSession is a browser session cookie
	CredentialSession Credential = "session"
	// CredentialAPIToken is an API token, which acts with its creator's role
	CredentialAPIToken Credential = "api_token"
	// CredentialOAuth is an access token an MCP client got from the identity provider, which acts as the user who signed in
	CredentialOAuth Credential = "oauth"
)

// Principal is who is calling and in which workspace
// Every field is comparable, so the auth middleware can tell when a revalidated credential changed
type Principal struct {
	WorkspaceID string
	// Credential is how the caller authenticated, and the zero value counts as no session so a principal built without it never passes as one
	Credential Credential
	// UserID is set for browser sessions and OAuth access tokens, and empty for API tokens
	UserID string
	// TokenID is set when the caller authenticated with an API token
	TokenID string
	// TokenCreatorID is the user who created the API token, whose role the token acts with
	TokenCreatorID string
	// LoginProvider is the ID of the sign-in provider a browser session or an OAuth access token comes from
	LoginProvider string
	// SessionExpiresAt is when a browser session ends in Unix seconds, which a session moved to another workspace keeps
	SessionExpiresAt int64
	// SessionID identifies a browser session, which a session moved to another workspace keeps so that signing out still ends it
	SessionID string
	// Role is what the caller may do in the workspace
	Role Role
	// InstanceAdmin is set for browser sessions of instance admins, who manage every user and workspace
	InstanceAdmin bool
}

// IsSession reports whether the caller signed in through the browser, which some operations require
func (p Principal) IsSession() bool {
	return p.Credential == CredentialSession
}

type ctxKey struct{}

// WithPrincipal returns a context carrying the principal
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal of the request, if authenticated
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// WorkspaceID returns the caller's workspace, or an empty string when unauthenticated
// Handlers behind the auth middleware can rely on it being set
func WorkspaceID(ctx context.Context) string {
	p, _ := From(ctx)
	return p.WorkspaceID
}

// UserIDOf returns the caller's user ID, which is empty for API tokens
func UserIDOf(ctx context.Context) string {
	p, _ := From(ctx)
	return p.UserID
}

// CallerID returns the person behind the call for per-person limits, which is the creator for an API token
func CallerID(ctx context.Context) string {
	p, _ := From(ctx)
	if p.UserID != "" {
		return p.UserID
	}
	return p.TokenCreatorID
}

// UserIDPtr returns the caller's user ID as a pointer suitable for nullable columns
func UserIDPtr(ctx context.Context) *string {
	p, _ := From(ctx)
	if p.UserID == "" {
		return nil
	}
	return new(p.UserID)
}

// RoleOf returns the caller's role in their workspace, or an empty role when unauthenticated
func RoleOf(ctx context.Context) Role {
	p, _ := From(ctx)
	return p.Role
}
