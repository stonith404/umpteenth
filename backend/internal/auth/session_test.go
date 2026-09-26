//go:build unit

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoginStateCookieIsNotASession(t *testing.T) {
	codec := &cookieCodec{key: []byte("0123456789abcdef0123456789abcdef")}
	svc := &Service{codec: codec}

	// Anyone who starts a login gets a login-state cookie signed by the server
	value, err := codec.encode(kindLoginState, loginState{State: "s", Nonce: "n", Verifier: "v", ExpiresAt: time.Now().Add(loginStateTTL).Unix()})
	require.NoError(t, err)

	_, err = svc.VerifySession(context.Background(), value)
	require.Error(t, err, "a login-state cookie must not pass as a session")

	// A real session still verifies
	session, err := codec.sessionCookie("user-1", "ws-1")
	require.NoError(t, err)
	p, err := svc.VerifySession(context.Background(), session.Value)
	require.NoError(t, err)
	require.Equal(t, "user-1", p.UserID)
}
