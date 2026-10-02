// Package middleware holds the cross-cutting Huma middlewares
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// SessionVerifier resolves a browser session cookie
type SessionVerifier interface {
	VerifySession(ctx context.Context, value string) (principal.Principal, error)
}

// TokenValidator resolves an API bearer token
type TokenValidator interface {
	ValidateAPIToken(ctx context.Context, token string) (principal.Principal, error)
	ValidateAPITokenID(ctx context.Context, workspaceID, tokenID string) error
}

type credentialRevalidator func(context.Context) error
type credentialRevalidatorKey struct{}

// RevalidateCredential confirms that the credential attached by Required is still usable
func RevalidateCredential(ctx context.Context) error {
	revalidate, _ := ctx.Value(credentialRevalidatorKey{}).(credentialRevalidator)
	if revalidate == nil {
		return nil
	}
	return revalidate(ctx)
}

type authenticatedKey struct{}

// authenticated is a caller another layer of this process already authenticated
type authenticated struct {
	principal  principal.Principal
	revalidate credentialRevalidator
}

// WithAuthenticated marks a request built inside the process for a caller that is already authenticated, such as the REST request behind an MCP tool call
// Only code in this process can put values on a request's context, so no client can reach this path
func WithAuthenticated(ctx context.Context, p principal.Principal, revalidate func(context.Context) error) context.Context {
	return context.WithValue(ctx, authenticatedKey{}, authenticated{principal: p, revalidate: revalidate})
}

// RateLimiter decides whether a request with the given key may proceed
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

// WorkspaceHeader names the workspace a browser tab shows, which the SPA sends with its requests, and the workspace an MCP client signed in through OAuth works in
const WorkspaceHeader = "X-Umpteenth-Workspace"

// Auth authenticates requests by session cookie or API token and puts the principal on the context
type Auth struct {
	sessions      SessionVerifier
	tokens        TokenValidator
	sessionCookie string
	appOrigin     string
}

func NewAuth(sessions SessionVerifier, tokens TokenValidator, sessionCookie, appURL string) *Auth {
	origin := ""
	if u, err := url.Parse(appURL); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	return &Auth{sessions: sessions, tokens: tokens, sessionCookie: sessionCookie, appOrigin: origin}
}

// Required rejects unauthenticated requests
func (a *Auth) Required() huma.Middlewares {
	return huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		p, viaCookie, revalidate, err := a.resolve(ctx)
		if err != nil {
			httpserver.WriteError(ctx, err)
			return
		}

		// Cookie-authenticated writes must come from our own origin, which stops CSRF without tokens
		if viaCookie && !isSafeMethod(ctx.Method()) && !a.sameOrigin(ctx) {
			httpserver.WriteError(ctx, apperror.Forbidden("Cross-site request rejected"))
			return
		}

		// Every tab of a browser shares the session cookie, so moving the session to another workspace in one tab moves it for all of them
		// A write from a tab that still shows the previous workspace would land in the new one, so it is refused until the tab reloads
		// Callers that don't name a workspace, such as scripts, keep acting on the session's one
		access := httpserver.AccessOf(ctx.Operation())
		shown := ctx.Header(WorkspaceHeader)
		if viaCookie && !isSafeMethod(ctx.Method()) && !access.AnyWorkspace && shown != "" && shown != p.WorkspaceID {
			httpserver.WriteError(ctx, apperror.WorkspaceChanged())
			return
		}

		// The operation may ask for more than a signed-in caller, such as a role or a session
		err = access.Check(p)
		if err != nil {
			httpserver.WriteError(ctx, err)
			return
		}

		requestCtx := principal.WithPrincipal(ctx.Context(), p)
		requestCtx = context.WithValue(requestCtx, credentialRevalidatorKey{}, revalidate)

		next(huma.WithContext(ctx, requestCtx))
	}}
}

func (a *Auth) resolve(ctx huma.Context) (principal.Principal, bool, credentialRevalidator, error) {
	// A caller authenticated in-process carries no credentials, and as it never comes with a cookie the CSRF and stale-tab checks don't apply
	if pre, ok := ctx.Context().Value(authenticatedKey{}).(authenticated); ok {
		return pre.principal, false, pre.revalidate, nil
	}

	// A Bearer header carries an API token, which wins over any cookie the client also sends
	authz := ctx.Header("Authorization")
	if token, ok := strings.CutPrefix(authz, "Bearer "); ok {
		p, err := a.tokens.ValidateAPIToken(ctx.Context(), strings.TrimSpace(token))
		if err != nil {
			return principal.Principal{}, false, nil, err
		}
		workspaceID, tokenID := p.WorkspaceID, p.TokenID
		return p, false, func(checkCtx context.Context) error {
			return a.tokens.ValidateAPITokenID(checkCtx, workspaceID, tokenID)
		}, nil
	}

	// Other schemes leave the session cookie in charge, since a browser behind a reverse proxy with Basic auth sends the proxy's credentials on every request
	cookie, err := huma.ReadCookie(ctx, a.sessionCookie)
	if err != nil || cookie.Value == "" {
		// Without a session an API client with a malformed Authorization header still learns that its token is the problem
		if authz != "" {
			return principal.Principal{}, false, nil, apperror.InvalidToken()
		}
		return principal.Principal{}, false, nil, apperror.NotSignedIn()
	}
	p, err := a.sessions.VerifySession(ctx.Context(), cookie.Value)
	if err != nil {
		return principal.Principal{}, true, nil, err
	}
	value := cookie.Value
	return p, true, func(checkCtx context.Context) error {
		current, err := a.sessions.VerifySession(checkCtx, value)
		if err != nil {
			return err
		}
		if current != p {
			return apperror.NotSignedIn()
		}
		return nil
	}, nil
}

func (a *Auth) sameOrigin(ctx huma.Context) bool {
	// Browsers send Sec-Fetch-Site on every request, and older ones at least send Origin on writes
	if site := ctx.Header("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := ctx.Header("Origin")
	return origin == "" || origin == a.appOrigin
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// RateLimit limits requests per client IP with the given limiter
// With trustProxy the IP comes from the hop the reverse proxy appended to X-Forwarded-For, otherwise from the connection
func RateLimit(limiter RateLimiter, name string, trustProxy bool) huma.Middlewares {
	return huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		// Hashing keeps the key within the characters the limiter accepts whatever a header contains
		sum := sha256.Sum256([]byte(clientIP(ctx, trustProxy)))
		key := name + "-" + hex.EncodeToString(sum[:16])
		allowed, retryAfter, err := limiter.Allow(ctx.Context(), key)
		switch {
		case err != nil:
			// A limiter outage must not lock everyone out, so the request goes through
			slog.WarnContext(ctx.Context(), "Rate limiter failed, letting the request through", slog.String("limiter", name), slog.Any("error", err))
		case !allowed:
			httpserver.WriteError(ctx, apperror.RateLimited(retryAfter))
			return
		}
		next(ctx)
	}}
}

func clientIP(ctx huma.Context, trustProxy bool) string {
	// A client can put anything into X-Forwarded-For, but a reverse proxy appends the address it saw as the last hop
	if trustProxy {
		if fwd := ctx.Header("X-Forwarded-For"); fwd != "" {
			last := fwd[strings.LastIndexByte(fwd, ',')+1:]
			if addr, err := netip.ParseAddr(strings.TrimSpace(last)); err == nil {
				return addr.String()
			}
		}
	}
	host := ctx.RemoteAddr()
	if addrPort, err := netip.ParseAddrPort(host); err == nil {
		return addrPort.Addr().String()
	}
	return host
}
