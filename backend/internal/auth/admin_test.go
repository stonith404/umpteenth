//go:build unit

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestAdminUsersNameTheProviderTheySignInWith(t *testing.T) {
	svc := newTestService(t,
		ProviderConfig{Type: TypeOIDC, ID: "pocket-id", Name: "Pocket ID", Issuer: "https://id.example.com/"},
		ProviderConfig{Type: TypeGitHub, ID: "github", Name: "GitHub"},
	)

	// The OIDC account's issuer lacks the configured trailing slash, and the last account's provider is no longer configured
	issuers := map[string]string{
		"a": "https://id.example.com",
		"b": "https://github.com",
		"c": "https://old.example.org/realms/team",
	}
	for name, issuer := range issuers {
		testutil.Exec(t, svc.db, "INSERT INTO users (id, issuer, subject, name, created_at) VALUES ($1, $2, $3, $4, $5)",
			database.NewID(), issuer, name, name, database.Now())
	}

	out, err := (&handler{service: svc}).listUsers(t.Context(), &listUsersInput{ListParams: httpserver.ListParams{Page: 1, PageSize: 25}})
	require.NoError(t, err)
	providers := map[string]string{}
	for _, u := range out.Body.Items {
		require.Equal(t, issuers[*u.Name], u.Issuer)
		providers[*u.Name] = u.Provider
	}
	require.Equal(t, map[string]string{"a": "Pocket ID", "b": "GitHub", "c": "old.example.org"}, providers)
}
