// Package principal carries the authenticated caller through a request context
package principal

import "context"

// Principal is who is calling and in which workspace
type Principal struct {
	WorkspaceID string
	// UserID is set for browser sessions and empty for API tokens
	UserID string
	// TokenID is set when the caller authenticated with an API token
	TokenID string
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

// UserIDPtr returns the caller's user ID as a pointer suitable for nullable columns
func UserIDPtr(ctx context.Context) *string {
	p, _ := From(ctx)
	if p.UserID == "" {
		return nil
	}
	id := p.UserID
	return &id
}
