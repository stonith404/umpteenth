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
	ctx := principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid})

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
