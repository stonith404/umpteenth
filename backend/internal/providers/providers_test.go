//go:build unit

package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

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
