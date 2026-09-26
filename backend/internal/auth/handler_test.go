//go:build unit

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

func TestSessionTellsEveryPageTheWorkspacesUsageUnit(t *testing.T) {
	svc := newTestService(t)
	units := settings.New(settings.Dependencies{DB: svc.db})
	h := &handler{service: svc, settings: units}
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: workspaces.DefaultID, TokenID: "token", Role: principal.RoleMember})

	// A workspace that never chose a unit shows prices
	out, err := h.me(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, settings.UsagePrice, out.Body.Workspace.UsageUnit)

	// The next load of the session picks up a switch to tokens
	require.NoError(t, units.Set(ctx, workspaces.DefaultID, "usageUnit", settings.UsageTokens))
	out, err = h.me(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, settings.UsageTokens, out.Body.Workspace.UsageUnit)
}
