//go:build unit

package providers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/openai"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// hugeModelList is far more models than any real server lists, but its body stays well under the 64 MB response cap
const hugeModelList = 500_000

// modelListBody builds a /models answer listing n distinct model IDs that start with prefix
func modelListBody(prefix string, n int) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"data":[`)
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"id":"%s%07d"}`, prefix, i)
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

// A server a workspace admin points a provider at can list any number of models
// Syncing them must neither hold SQLite's single write lock long enough to fail other workspaces' writes nor store every entry
func TestServerSyncOfAHugeModelListDoesNotLockOutOtherWorkspaces(t *testing.T) {
	ctx := context.Background()

	// A file database behaves like production, where writers wait on the WAL write lock for the busy timeout
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := database.Open(ctx, database.EngineSQLite, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.Migrate(ctx, db))
	wid := testutil.SeedWorkspace(t, db)
	other := testutil.SeedWorkspace(t, db)
	factories := map[string]Factory{llm.KindOpenAI: func(cfg llm.Config) (llm.Provider, error) { return openai.New(cfg) }}
	s := newService(db, []byte("0123456789abcdef0123456789abcdef"), factories)

	// The attacker's server answers the model list with a huge but byte-wise acceptable list
	body := modelListBody("m", hugeModelList)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	pid, err := s.CreateProvider(ctx, wid, "Huge", llm.KindOpenAI, srv.URL+"/v1", "", nil)
	require.NoError(t, err)

	// A second handle that never waits tells when someone holds the write lock
	probe, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)&_txlock=immediate")
	require.NoError(t, err)
	t.Cleanup(func() { _ = probe.Close() })
	probe.SetMaxOpenConns(1)
	locked := func() bool {
		tx, err := probe.BeginTx(ctx, nil)
		if err != nil {
			return true
		}
		_ = tx.Rollback()
		return false
	}

	// Run the sync in the background
	type outcome struct {
		result SyncResult
		err    error
	}
	synced := make(chan outcome, 1)
	go func() {
		result, err := s.SyncProvider(ctx, wid, pid)
		synced <- outcome{result, err}
	}()

	// Once the sync holds the write lock, another workspace writes a setting and records how long the lock stayed held
	var otherErr error
	var otherTook, longestHold time.Duration
	var wg sync.WaitGroup
	wg.Go(func() {
		var heldSince time.Time
		wrote := false
		for {
			select {
			case o := <-synced:
				synced <- o
				if !heldSince.IsZero() {
					longestHold = max(longestHold, time.Since(heldSince))
				}
				return
			default:
			}
			if locked() {
				if heldSince.IsZero() {
					heldSince = time.Now()
				}
				if !wrote {
					wrote = true
					writeStart := time.Now()
					_, otherErr = db.ExecContext(ctx, `INSERT INTO settings (workspace_id, key, value) VALUES ($1, 'probe', '1')`, other)
					otherTook = time.Since(writeStart)
				}
			} else if !heldSince.IsZero() {
				longestHold = max(longestHold, time.Since(heldSince))
				heldSince = time.Time{}
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	wg.Wait()
	o := <-synced
	require.NoError(t, o.err)

	// Other workspaces keep writing while the sync runs, and the sync never holds the lock for seconds
	assert.NoError(t, otherErr, "another workspace's write must not fail while a model sync runs, it waited %s", otherTook)
	assert.Less(t, longestHold, 2*time.Second, "a model sync must not hold the database write lock for seconds")

	// A list this size is refused and recorded on the provider rather than stored
	var stored int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE workspace_id = $1 AND provider_id = $2`, wid, pid).Scan(&stored))
	assert.Zero(t, stored, "a model list far beyond any real server must not be stored")
	assert.NotEmpty(t, o.result.Error)
}

// Models a server stops listing are kept, so a server that lists new IDs on every sync must not add rows without end
func TestServerSyncWithRotatingModelIDsStopsAtTheBound(t *testing.T) {
	ctx := context.Background()
	s, db, wid := newTestService(t)
	stored := func(pid string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE workspace_id = $1 AND provider_id = $2`, wid, pid).Scan(&n))
		return n
	}

	// Every answer lists a fresh set of IDs, each within the bound but two of them past it
	perSync := maxProviderModels * 2 / 3
	var round atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(modelListBody(fmt.Sprintf("r%d-", round.Load()), perSync))
	}))
	t.Cleanup(srv.Close)
	pid, err := s.CreateProvider(ctx, wid, "Rotating", llm.KindOpenAI, srv.URL+"/v1", "", nil)
	require.NoError(t, err)

	// The first list is added in full
	result, err := s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Added: perSync}, result)

	// The next one would take the provider past the bound, so it changes nothing and leaves the reason on the provider
	round.Store(1)
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Error: errTooManyModels.Error()}, result)
	assert.Equal(t, perSync, stored(pid))
	p, err := s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: pid})
	require.NoError(t, err)
	require.NotNil(t, p.SyncError)

	// Listing the models the provider already holds still syncs, and the error clears
	round.Store(0)
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{}, result)
	p, err = s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: pid})
	require.NoError(t, err)
	assert.Nil(t, p.SyncError)
}

// A server can make each entry of its list as large as the response limit allows, so the entries themselves are bounded too
func TestServerSyncBoundsTheSizeOfEachModel(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	factories := map[string]Factory{llm.KindOpenAI: func(cfg llm.Config) (llm.Provider, error) { return openai.New(cfg) }}
	s := newService(db, []byte("0123456789abcdef0123456789abcdef"), factories)

	// The server lists one model with an ID of several megabytes and one with a name of several megabytes
	hugeID := strings.Repeat("i", 4<<20)
	hugeName := strings.Repeat("n", 4<<20)
	body := fmt.Sprintf(`{"data":[{"id":%q},{"id":"real-model","name":%q}]}`, hugeID, hugeName)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	pid, err := s.CreateProvider(ctx, wid, "Big entries", llm.KindOpenAI, srv.URL+"/v1", "", nil)
	require.NoError(t, err)

	result, err := s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	require.Empty(t, result.Error)

	// The overlong ID is skipped and the overlong name is cut
	rows, err := providersdb.New(db).ListProviderModels(ctx, providersdb.ListProviderModelsParams{WorkspaceID: wid, ProviderID: pid})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "real-model", rows[0].Model)
	require.NotNil(t, rows[0].Label)
	assert.Len(t, *rows[0].Label, maxModelLabelChars)
}
