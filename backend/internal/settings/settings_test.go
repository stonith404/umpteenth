//go:build unit

package settings

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// fakeModels knows a fixed set of model IDs
type fakeModels map[string]bool

func (f fakeModels) ModelExists(_ context.Context, _, id string) (bool, error) {
	return f[id], nil
}

func TestUpdateRejectsUnknownModelsAndResetsTheImage(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	m := New(Dependencies{DB: db, Defaults: Defaults{Image: "default-image", RetentionDays: 90}})
	m.SetModels(fakeModels{"known": true})
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})

	// A model the workspace doesn't have is refused and nothing is saved
	in := &updateInput{}
	in.Body.AgentModelID = new("missing")
	_, err := m.update(ctx, in)
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
	s, err := m.Get(ctx, wid)
	require.NoError(t, err)
	require.Nil(t, s.AgentModelID)

	// A known model is saved, and an empty image goes back to the server default
	in = &updateInput{}
	in.Body.AgentModelID = new("known")
	in.Body.DefaultImage = new("custom:1")
	_, err = m.update(ctx, in)
	require.NoError(t, err)
	in = &updateInput{}
	in.Body.DefaultImage = new("  ")
	out, err := m.update(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "known", *out.Body.AgentModelID)
	require.Equal(t, "default-image", out.Body.DefaultImage)
}

func TestUsageUnitDefaultsToPriceAndSwitchesToTokens(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	other := testutil.SeedWorkspace(t, db)
	m := New(Dependencies{DB: db})
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})

	// A workspace that never chose a unit shows prices
	unit, err := m.UsageUnit(ctx, wid)
	require.NoError(t, err)
	require.Equal(t, UsagePrice, unit)

	// Switching to tokens changes only this workspace
	in := &updateInput{}
	in.Body.UsageUnit = new(UsageTokens)
	out, err := m.update(ctx, in)
	require.NoError(t, err)
	require.Equal(t, UsageTokens, out.Body.UsageUnit)
	unit, err = m.UsageUnit(ctx, wid)
	require.NoError(t, err)
	require.Equal(t, UsageTokens, unit)
	unit, err = m.UsageUnit(ctx, other)
	require.NoError(t, err)
	require.Equal(t, UsagePrice, unit)

	// A saved value the UI doesn't know reads as prices
	require.NoError(t, m.Set(ctx, other, "usageUnit", "credits"))
	unit, err = m.UsageUnit(ctx, other)
	require.NoError(t, err)
	require.Equal(t, UsagePrice, unit)
}
