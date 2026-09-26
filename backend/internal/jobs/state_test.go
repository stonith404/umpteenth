//go:build unit

package jobs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestStateStopsAcceptingNewKeysAtTheLimit(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	jobID := testutil.SeedJob(t, db, testutil.SeedWorkspace(t, db), ConcurrencySkip)
	store := &StateStore{queries: jobsdb.New(db), jobID: jobID}
	ctx := context.Background()

	// Fill the state up to the limit in one transaction
	err := db.InTx(ctx, func(tx *database.Tx) error {
		for i := range maxStateKeys {
			_, err := tx.ExecContext(ctx, "INSERT INTO job_state (job_id, key, value, updated_at) VALUES ($1, $2, 'v', 0)", jobID, fmt.Sprintf("k%d", i))
			if err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	// A new key is refused, while an existing key can still be updated
	err = store.Set(ctx, "one-too-many", "v")
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
	require.NoError(t, store.Set(ctx, "k7", "updated"))
}
