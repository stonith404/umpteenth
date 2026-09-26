//go:build unit

package settings

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// allowAllURLs lets every webhook URL through the egress guard
type allowAllURLs struct{}

func (allowAllURLs) CheckURL(context.Context, string, string) error { return nil }

// A chat webhook URL is the credential to post into the channel, so only those who may change it read it whole
func TestMembersDoNotReadTheNotificationWebhookURL(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	m := New(Dependencies{DB: db, URLs: allowAllURLs{}})
	admin := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid, UserID: "admin", Role: principal.RoleAdmin})
	member := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid, UserID: "member", Role: principal.RoleMember})

	// An admin sets a Slack webhook, whose path is its secret
	const url = "https://hooks.slack.com/services/T000/B000/XXXXXXXXXXXXXXXXXXXXXXXX"
	in := &updateInput{}
	in.Body.NotifyWebhookURL = new(url)
	_, err := m.update(admin, in)
	require.NoError(t, err)

	// Admins read it whole, since they edit it
	out, err := m.get(admin, nil)
	require.NoError(t, err)
	require.Equal(t, url, *out.Body.NotifyWebhookURL)

	// Members only learn where notifications go
	out, err = m.get(member, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Body.NotifyWebhookURL)
	require.NotContains(t, *out.Body.NotifyWebhookURL, "XXXXXXXX")
	require.Equal(t, "https://hooks.slack.com/…", *out.Body.NotifyWebhookURL)
}
