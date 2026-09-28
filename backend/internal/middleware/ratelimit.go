package middleware

import (
	"context"
	"log/slog"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// CheckRateLimit asks the limiter whether a call under key may proceed, for handlers that limit only some of their calls or key them by workspace
// A nil limiter admits everything, and an outage lets the call through like the RateLimit middleware does
func CheckRateLimit(ctx context.Context, limiter RateLimiter, key string) error {
	if limiter == nil {
		return nil
	}
	allowed, retryAfter, err := limiter.Allow(ctx, key)
	switch {
	case err != nil:
		slog.WarnContext(ctx, "Rate limiter failed, letting the call through", slog.String("key", key), slog.Any("error", err))
	case !allowed:
		return apperror.RateLimited(retryAfter)
	}
	return nil
}
