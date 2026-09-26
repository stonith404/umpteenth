// Package middleware holds the cross-cutting Huma middlewares
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
}

// RateLimiter decides whether a request with the given key may proceed
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

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
		p, viaCookie, err := a.resolve(ctx)
		if err != nil {
			httpserver.WriteError(ctx, err)
			return
		}

		// Cookie-authenticated writes must come from our own origin, which stops CSRF without tokens
		if viaCookie && !isSafeMethod(ctx.Method()) && !a.sameOrigin(ctx) {
			httpserver.WriteError(ctx, apperror.Forbidden("Cross-site request rejected"))
			return
		}

		next(huma.WithContext(ctx, principal.WithPrincipal(ctx.Context(), p)))
	}}
}

func (a *Auth) resolve(ctx huma.Context) (principal.Principal, bool, error) {
	if authz := ctx.Header("Authorization"); authz != "" {
		token, ok := strings.CutPrefix(authz, "Bearer ")
		if !ok {
			return principal.Principal{}, false, apperror.InvalidToken()
		}
		p, err := a.tokens.ValidateAPIToken(ctx.Context(), strings.TrimSpace(token))
		return p, false, err
	}

	cookie, err := huma.ReadCookie(ctx, a.sessionCookie)
	if err != nil || cookie.Value == "" {
		return principal.Principal{}, false, apperror.NotSignedIn()
	}
	p, err := a.sessions.VerifySession(ctx.Context(), cookie.Value)
	return p, true, err
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
	if limiter == nil {
		return nil
	}
	return huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		// Hashing keeps the key within the characters the limiter accepts whatever a header contains
		sum := sha256.Sum256([]byte(clientIP(ctx, trustProxy)))
		key := name + "-" + hex.EncodeToString(sum[:16])
		allowed, retryAfter, err := limiter.Allow(ctx.Context(), key)
		if err == nil && !allowed {
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
			hops := strings.Split(fwd, ",")
			if addr, err := netip.ParseAddr(strings.TrimSpace(hops[len(hops)-1])); err == nil {
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
