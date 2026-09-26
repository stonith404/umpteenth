//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

// keyRecorder allows everything and remembers the keys it was asked about
type keyRecorder struct{ keys []string }

func (k *keyRecorder) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	k.keys = append(k.keys, key)
	return true, 0, nil
}

func TestRateLimitKeysIgnoreForgedForwardedFor(t *testing.T) {
	for _, trustProxy := range []bool{false, true} {
		limiter := &keyRecorder{}
		_, api := humatest.New(t)
		huma.Register(api, huma.Operation{Method: http.MethodGet, Path: "/x", Middlewares: RateLimit(limiter, "login", trustProxy)},
			func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })

		// The same client rotates the first hop, which a proxy that appends the real address does not change
		for _, forged := range []string{"1.1.1.1", "2.2.2.2", "5/6"} {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.RemoteAddr = "10.0.0.9:4321"
			req.Header.Set("X-Forwarded-For", forged+", 203.0.113.7")
			api.Adapter().ServeHTTP(httptest.NewRecorder(), req)
		}
		require.Len(t, limiter.keys, 3)
		require.Equal(t, limiter.keys[0], limiter.keys[1], "trustProxy=%v", trustProxy)
		require.Equal(t, limiter.keys[0], limiter.keys[2], "trustProxy=%v", trustProxy)
		require.NotContains(t, limiter.keys[0], "/")
	}
}
