//go:build unit

// Package testutil holds helpers that are only compiled into unit tests
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/italypaleale/francis/components/standalone"
	"github.com/italypaleale/francis/host/local"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
)

// NewDatabaseForTest returns a migrated, empty database for one test
// TEST_DB=postgres runs against TEST_POSTGRES_URL (a server where the user may create databases), so CI can run every test on both engines
func NewDatabaseForTest(t *testing.T) *database.DB {
	t.Helper()
	ctx := t.Context()

	var db *database.DB
	var err error
	switch os.Getenv("TEST_DB") {
	case "postgres":
		db = newPostgresForTest(t)
	default:
		// A uniquely named shared-cache memory database keeps every test isolated but lets the pool share it
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
		db, err = database.Open(ctx, database.EngineSQLite, fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", name, time.Now().UnixNano()))
		require.NoError(t, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, database.Migrate(ctx, db))
	return db
}

func newPostgresForTest(t *testing.T) *database.DB {
	t.Helper()
	ctx := t.Context()
	adminURL := os.Getenv("TEST_POSTGRES_URL")
	if adminURL == "" {
		adminURL = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" // #nosec G101 -- local test database default
	}

	// Each test gets its own database, dropped when the test ends
	admin, err := pgxpool.New(ctx, adminURL)
	require.NoError(t, err)
	// The name is random, since go test runs packages in parallel against the same server and clock readings repeat across them
	dbName := "umptest_" + strings.ReplaceAll(database.NewID(), "-", "")
	_, err = admin.Exec(ctx, "CREATE DATABASE "+dbName)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		admin.Close()
	})

	// pgx's ConnString returns the original string, so the database is swapped in the URL itself
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	u.Path = "/" + dbName
	db, err := database.Open(ctx, database.EnginePostgres, u.String())
	require.NoError(t, err)

	var current string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&current))
	require.Equal(t, dbName, current, "tests must run in their own database")
	return db
}

// testActorHostPSK is the runtime pre-shared key for test actor hosts, which never talk to another host
const testActorHostPSK = "umpteenth-test-actor-host-psk-32bytes"

// NewActorHostForTest starts a single-host Francis cluster on the in-memory provider
// The register callback runs before the host starts, so tests can register actors
func NewActorHostForTest(t *testing.T, register func(t *testing.T, h *local.Host)) *local.Host {
	t.Helper()

	h, err := local.NewHost(
		local.WithAddress(freeLoopbackUDPAddr(t)),
		local.WithRuntimePSKs([]byte(testActorHostPSK)),
		local.WithStandaloneMemoryProvider(standalone.StandaloneMemoryOptions{}),
		local.WithShutdownGracePeriod(time.Second),
	)
	require.NoError(t, err)
	if register != nil {
		register(t, h)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	select {
	case <-h.Ready():
	case err := <-errCh:
		t.Fatalf("actor host stopped before becoming ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the actor host")
	}
	// The peer server starts right after the ready signal, which a very fast test could race with on cleanup
	time.Sleep(100 * time.Millisecond)
	return h
}

func freeLoopbackUDPAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.LocalAddr().String()
	require.NoError(t, lis.Close())
	return addr
}

// Exec runs a statement and fails the test on error, for seeding data
func Exec(t *testing.T, db *database.DB, query string, args ...any) sql.Result {
	t.Helper()
	res, err := db.ExecContext(t.Context(), query, args...)
	require.NoError(t, err)
	return res
}

// SeedWorkspace creates a workspace and returns its ID
func SeedWorkspace(t *testing.T, db *database.DB) string {
	t.Helper()
	id := database.NewID()
	Exec(t, db, "INSERT INTO workspaces (id, name, created_at) VALUES ($1, $2, $3)", id, "Test", database.Now())
	return id
}

// SeedUser creates a user and returns its ID
func SeedUser(t *testing.T, db *database.DB) string {
	t.Helper()
	id := database.NewID()
	Exec(t, db, "INSERT INTO users (id, issuer, subject, email, name, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		id, "https://issuer.test", id, id+"@example.com", "User "+id, database.Now())
	return id
}

// SeedMember makes the user a member of the workspace with the role
func SeedMember(t *testing.T, db *database.DB, workspaceID, userID, role string) {
	t.Helper()
	Exec(t, db, "INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)", workspaceID, userID, role, database.Now())
}

// SeedJob creates a job with the given concurrency policy and returns its ID
func SeedJob(t *testing.T, db *database.DB, workspaceID, concurrency string) string {
	t.Helper()
	id := database.NewID()
	now := database.Now()
	Exec(t, db, `INSERT INTO jobs (id, workspace_id, name, instruction, concurrency, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id, workspaceID, "Test job", "Do the thing", concurrency, now)
	return id
}
