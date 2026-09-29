//go:build unit

package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestDeletedModelsLeaveTheDefaultModels(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	testutil.Exec(t, db, `INSERT INTO providers (id, workspace_id, name, kind, created_at) VALUES ('p1', $1, 'P', 'openai', $2)`, wid, database.Now())
	for _, id := range []string{"m-kept", "m-gone"} {
		testutil.Exec(t, db, `INSERT INTO models (id, workspace_id, provider_id, model, created_at) VALUES ($1, $2, 'p1', $1, $3)`, id, wid, database.Now())
	}
	testutil.Exec(t, db, `INSERT INTO settings (workspace_id, key, value) VALUES ($1, 'agentModelId', '"m-kept"'), ($1, 'reflectionModelId', '"m-gone"'), ($1, 'defaultImage', '"img"')`, wid)

	q := providersdb.New(db)
	_, err := q.DeleteModel(context.Background(), providersdb.DeleteModelParams{WorkspaceID: wid, ID: "m-gone"})
	require.NoError(t, err)
	require.NoError(t, q.ClearMissingModelSettings(context.Background(), wid))

	// Only the setting that pointed at the deleted model is gone
	var keys []string
	rows, err := db.QueryContext(context.Background(), `SELECT key FROM settings WHERE workspace_id = $1 ORDER BY key`, wid)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		keys = append(keys, k)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"agentModelId", "defaultImage"}, keys)
}

// allowAllEgress is an egress guard that lets every URL through
type allowAllEgress struct{}

func (allowAllEgress) CheckURL(context.Context, string, string) error { return nil }

func TestBaseURLMustNotCarryCredentials(t *testing.T) {
	m := &Module{deps: Dependencies{Egress: allowAllEgress{}}}
	require.NoError(t, m.checkBaseURL(context.Background(), "https://proxy.example/v1"))
	for _, baseURL := range []string{"https://user:secret@proxy.example/v1", "https://token@proxy.example/v1"} {
		err := m.checkBaseURL(context.Background(), baseURL)
		appErr, ok := apperror.As(err)
		require.True(t, ok, baseURL)
		require.Equal(t, apperror.CodeValidationFailed, appErr.Code(), baseURL)
	}
}
