//go:build unit

package jobs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func newStateStoreForTest(t *testing.T, db *database.DB) *StateStore {
	jobID := testutil.SeedJob(t, db, testutil.SeedWorkspace(t, db), ConcurrencySkip)
	return &StateStore{db: db, queries: jobsdb.New(db), locks: &stateLocks{}, jobID: jobID}
}

// databaseForConcurrentWrites returns a database that makes concurrent transactions wait like in production
// An in-memory SQLite database fails fast on locks instead, so SQLite runs on a file
func databaseForConcurrentWrites(t *testing.T) *database.DB {
	db := testutil.NewDatabaseForTest(t)
	if db.Engine() != database.EngineSQLite {
		return db
	}
	db, err := database.Open(t.Context(), database.EngineSQLite, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.Migrate(t.Context(), db))
	return db
}

// fillState writes keys straight into the table, the way state from before a limit existed could look
func fillState(t *testing.T, db *database.DB, jobID, prefix string, n int, value string) {
	ctx := context.Background()
	err := db.InTx(ctx, func(tx *database.Tx) error {
		for i := range n {
			_, err := tx.ExecContext(ctx, "INSERT INTO job_state (job_id, key, value, updated_at) VALUES ($1, $2, $3, 0)", jobID, fmt.Sprintf("%s%03d", prefix, i), value)
			if err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
}

func TestStateStopsAcceptingNewKeysAtTheLimit(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	store := newStateStoreForTest(t, db)
	ctx := context.Background()
	fillState(t, db, store.jobID, "k", runner.MaxStateKeys, "v")

	// A new key is refused, while an existing key can still be updated
	err := store.Set(ctx, "one-too-many", "v")
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
	require.NoError(t, store.Set(ctx, "k007", "updated"))
}

func TestStateStopsGrowingAtTheSizeLimit(t *testing.T) {
	store := newStateStoreForTest(t, testutil.NewDatabaseForTest(t))
	ctx := context.Background()
	mib := strings.Repeat("<", 1<<20)

	// Fifteen values of 1 MiB fit, and a sixteenth would take keys and values past 16 MiB
	for i := range 15 {
		require.NoError(t, store.Set(ctx, fmt.Sprintf("k%02d", i), mib))
	}
	err := store.Set(ctx, "k15", mib)
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
	assert.Contains(t, err.Error(), "may total at most 16 MiB")

	// Sizes count in bytes, so multi-byte characters can't slip past the limit
	err = store.Set(ctx, "k15", strings.Repeat("ü", 1<<19))
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)

	// Replacing a value only counts its growth, so shrinking one key makes room for another
	require.NoError(t, store.Set(ctx, "k00", mib))
	require.NoError(t, store.Set(ctx, "k00", "small"))
	require.NoError(t, store.Set(ctx, "k15", mib))

	// Listing returns the whole state, which stays within the limit
	all, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 16)
}

func TestStateOverTheSizeLimitCanOnlyShrink(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	store := newStateStoreForTest(t, db)
	ctx := context.Background()
	mib := strings.Repeat("a", 1<<20)
	fillState(t, db, store.jobID, "k", 17, mib)

	// A state that grew past the limit before it was enforced isn't listed at once, and doesn't grow further
	_, err := store.List(ctx)
	require.True(t, apperror.IsCode(err, apperror.CodeConflict), err)
	err = store.Set(ctx, "new", "v")
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
	err = store.Set(ctx, "k000", mib+"a")
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)

	// Writes that don't grow it still work, so it can shrink back under the limit
	require.NoError(t, store.Set(ctx, "k000", mib))
	require.NoError(t, store.Set(ctx, "k000", ""))
	require.NoError(t, store.Set(ctx, "k001", ""))
	all, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 17)
}

func TestStateLimitsHoldForConcurrentWrites(t *testing.T) {
	ctx := context.Background()

	// setAll writes every key at once and returns how many writes got through, failing on anything but a refusal
	// Each write comes from a replica of its own, so only the database keeps them apart
	setAll := func(store *StateStore, keys []string, value string) int {
		var (
			mu       sync.Mutex
			accepted int
			wg       sync.WaitGroup
		)
		for _, key := range keys {
			replica := *store
			replica.locks = &stateLocks{}
			wg.Go(func() {
				err := replica.Set(ctx, key, value)
				if err != nil {
					assert.True(t, apperror.IsCode(err, apperror.CodeValidationFailed), err)
					return
				}
				mu.Lock()
				accepted++
				mu.Unlock()
			})
		}
		wg.Wait()
		return accepted
	}
	keys := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%03d", prefix, i)
		}
		return out
	}

	// New keys racing for the last free places don't take the state past its key limit
	db := databaseForConcurrentWrites(t)
	store := newStateStoreForTest(t, db)
	fillState(t, db, store.jobID, "k", runner.MaxStateKeys-10, "v")
	assert.Equal(t, 10, setAll(store, keys("race", 50), "v"))
	usage, err := store.queries.StateUsage(ctx, store.jobID)
	require.NoError(t, err)
	assert.EqualValues(t, runner.MaxStateKeys, usage.Keys)

	// Large values racing for the last free megabytes don't take it past its size limit
	store = newStateStoreForTest(t, db)
	fillState(t, db, store.jobID, "k", 14, strings.Repeat("a", 1<<20))
	assert.Equal(t, 1, setAll(store, keys("race", 8), strings.Repeat("a", 1<<20)))
	usage, err = store.queries.StateUsage(ctx, store.jobID)
	require.NoError(t, err)
	assert.LessOrEqual(t, usage.Bytes, int64(runner.MaxStateBytes))
}

func TestStateWritesOfAJobTakeTurnsOnAReplica(t *testing.T) {
	ctx := context.Background()
	var locks stateLocks

	// A second writer of the same job waits, while a writer of another job goes ahead
	unlock, err := locks.lock(ctx, "a")
	require.NoError(t, err)
	other, err := locks.lock(ctx, "b")
	require.NoError(t, err)
	other()
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = locks.lock(waiting, "a")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The next writer gets its turn once the first one is done, and the job's entry goes away with its last writer
	unlock()
	unlock, err = locks.lock(ctx, "a")
	require.NoError(t, err)
	unlock()
	assert.Empty(t, locks.jobs)

	// A write waits for its turn before it touches the database, so writers queued behind another hold no connection
	store := newStateStoreForTest(t, testutil.NewDatabaseForTest(t))
	unlock, err = store.locks.lock(ctx, store.jobID)
	require.NoError(t, err)
	waiting, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, store.Set(waiting, "k", "v"), context.DeadlineExceeded)
	unlock()
	_, found, err := store.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, found)
}
