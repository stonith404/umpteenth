//go:build unit

package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

func TestLoginStateCookieIsNotASession(t *testing.T) {
	s := newSessionStack(t, false, nil)
	svc := s.auth.service

	// Anyone who starts a login gets a login-state cookie signed by the server
	_, loginCookie, err := svc.BeginLogin(t.Context(), "a", "/")
	require.NoError(t, err)
	_, err = svc.VerifySession(t.Context(), loginCookie.Value)
	require.Error(t, err, "a login-state cookie must not pass as a session")

	// A real session still verifies
	session, err := s.signIn(t)
	require.NoError(t, err)
	p, err := svc.VerifySession(t.Context(), session)
	require.NoError(t, err)
	require.NotEmpty(t, p.UserID)
	require.Equal(t, "a", p.LoginProvider)
}

func TestSigningOutEndsEveryCopyOfTheSession(t *testing.T) {
	s := newSessionStack(t, true, nil)
	session, err := s.signIn(t)
	require.NoError(t, err)
	otherBrowser, err := s.signIn(t)
	require.NoError(t, err)

	// The session moves into a new workspace, while a copy of the cookie from before still works
	status, moved := s.post(t, "/api/workspaces", `{"name":"Side project"}`, session)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, moved)
	_, err = s.auth.VerifySession(t.Context(), session)
	require.NoError(t, err)

	// Signing out clears the cookie and ends the session, so neither of its cookies works anymore
	status, cleared := s.post(t, "/api/auth/logout", "", moved.Value)
	require.Less(t, status, 300)
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)
	for _, value := range []string{session, moved.Value} {
		_, err = s.auth.VerifySession(t.Context(), value)
		require.True(t, apperror.IsCode(err, apperror.CodeNotSignedIn), "got %v", err)
	}

	// A session the user started in another browser stays
	_, err = s.auth.VerifySession(t.Context(), otherBrowser)
	require.NoError(t, err)
}

func TestReactivatingAUserDoesntBringBackTheirSessions(t *testing.T) {
	s := newSessionStack(t, false, nil)
	session, err := s.signIn(t)
	require.NoError(t, err)
	p, err := s.auth.VerifySession(t.Context(), session)
	require.NoError(t, err)

	// An instance admin deactivates the user and reactivates them later
	admin := principal.WithPrincipal(t.Context(), principal.Principal{UserID: "instance-admin", InstanceAdmin: true})
	for _, deactivated := range []bool{true, false} {
		in := &updateUserInput{ID: p.UserID}
		in.Body.Deactivated = deactivated
		_, err = s.auth.handler.updateUser(admin, in)
		require.NoError(t, err)
	}

	// The session from before stays ended, and a new sign-in works
	_, err = s.auth.VerifySession(t.Context(), session)
	require.True(t, apperror.IsCode(err, apperror.CodeNotSignedIn), "got %v", err)
	session, err = s.signIn(t)
	require.NoError(t, err)
	_, err = s.auth.VerifySession(t.Context(), session)
	require.NoError(t, err)
}
