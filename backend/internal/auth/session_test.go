//go:build unit

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// memberEverywhere lets every user into every workspace as a member
type memberEverywhere struct{ WorkspaceResolver }

func (memberEverywhere) Access(context.Context, string, string) (workspaces.Access, error) {
	return workspaces.Access{Role: principal.RoleMember}, nil
}

func TestLoginStateCookieIsNotASession(t *testing.T) {
	codec := &cookieCodec{key: []byte("0123456789abcdef0123456789abcdef")}
	svc := &Service{codec: codec, workspaces: memberEverywhere{}}

	// Anyone who starts a login gets a login-state cookie signed by the server
	value, err := codec.encode(kindLoginState, loginState{State: "s", Nonce: "n", Verifier: "v", ExpiresAt: time.Now().Add(loginStateTTL).Unix()})
	require.NoError(t, err)

	_, err = svc.VerifySession(t.Context(), value)
	require.Error(t, err, "a login-state cookie must not pass as a session")

	// A real session still verifies
	session, err := codec.sessionCookie("user-1", "ws-1", "pocket-id", time.Now().Add(sessionTTL))
	require.NoError(t, err)
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)
	require.Equal(t, "user-1", p.UserID)
	require.Equal(t, "pocket-id", p.LoginProvider)
	require.Equal(t, principal.RoleMember, p.Role)
}
