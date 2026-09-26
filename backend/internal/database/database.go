package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Engine string

const (
	EngineSQLite   Engine = "sqlite"
	EnginePostgres Engine = "postgres"
)

// DBTX is the interface sqlc-generated code depends on
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is the application's database handle for either engine
// It implements DBTX so sqlc-generated code runs unchanged on SQLite and Postgres
type DB struct {
	engine Engine
	raw    *sql.DB
	pool   *pgxpool.Pool
}

// Open connects to the database described by the connection string
func Open(ctx context.Context, engine Engine, connString string) (*DB, error) {
	switch engine {
	case EngineSQLite:
		return openSQLite(connString)
	case EnginePostgres:
		return openPostgres(ctx, connString)
	default:
		return nil, fmt.Errorf("unknown database engine %q", engine)
	}
}

func openSQLite(connString string) (*DB, error) {
	// Create the parent directory for file databases so a fresh data dir works out of the box
	if !strings.HasPrefix(connString, "file:") && !strings.Contains(connString, ":memory:") {
		err := os.MkdirAll(filepath.Dir(connString), 0o750)
		if err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
		connString = "file:" + connString
	}

	// WAL and a busy timeout let the reader pool and the writer coexist, and immediate transactions avoid upgrade deadlocks
	// Francis expects temp_store=MEMORY on every connection, because it cannot change it inside its own transactions
	// Memory-mapped reads and a 32 MB page cache keep list scans over large run tables off the syscall path
	sep := "?"
	if strings.Contains(connString, "?") {
		sep = "&"
	}
	dsn := connString + sep + "_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(268435456)&_pragma=cache_size(-32768)&_txlock=immediate"

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	raw.SetMaxOpenConns(8)
	raw.SetConnMaxIdleTime(5 * time.Minute)

	return &DB{engine: EngineSQLite, raw: raw}, nil
}

func openPostgres(ctx context.Context, connString string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("failed to create Postgres pool: %w", err)
	}

	// Fail fast when the database is unreachable instead of on the first request
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = pool.Ping(pingCtx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to connect to Postgres: %w", err)
	}

	return &DB{engine: EnginePostgres, raw: stdlib.OpenDBFromPool(pool), pool: pool}, nil
}

// Engine returns which database engine backs this handle
func (d *DB) Engine() Engine { return d.engine }

// Raw returns the underlying database/sql handle, which does not rewrite placeholders
func (d *DB) Raw() *sql.DB { return d.raw }

// Pool returns the pgx pool on Postgres, and nil on SQLite
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Close releases all connections
func (d *DB) Close() error {
	err := d.raw.Close()
	if d.pool != nil {
		d.pool.Close()
	}
	return err
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.raw.ExecContext(ctx, d.rewrite(query), args...)
}

func (d *DB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return d.raw.PrepareContext(ctx, d.rewrite(query))
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.raw.QueryContext(ctx, d.rewrite(query), args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.raw.QueryRowContext(ctx, d.rewrite(query), args...)
}

// Tx is a transaction that rewrites placeholders like DB does
type Tx struct {
	db  *DB
	raw *sql.Tx
}

func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.raw.ExecContext(ctx, t.db.rewrite(query), args...)
}

func (t *Tx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return t.raw.PrepareContext(ctx, t.db.rewrite(query))
}

func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.raw.QueryContext(ctx, t.db.rewrite(query), args...)
}

func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.raw.QueryRowContext(ctx, t.db.rewrite(query), args...)
}

// InTx runs fn in a transaction, committing on success and rolling back on error or panic
func (d *DB) InTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	raw, err := d.raw.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = raw.Rollback()
			panic(p)
		}
		if err != nil {
			_ = raw.Rollback()
		}
	}()

	err = fn(&Tx{db: d, raw: raw})
	if err != nil {
		return err
	}

	err = raw.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// rewriteCache holds rewritten SQLite statements, since sqlc queries are a small fixed set of constant strings
// Dynamically built statements such as filtered lists also pass through here, so the cache stops growing at maxRewriteCache entries
var (
	rewriteCache     sync.Map
	rewriteCacheSize atomic.Int64
)

// maxRewriteCache comfortably covers every sqlc query plus the common list variants, while bounding memory under arbitrary request shapes
const maxRewriteCache = 2048

// rewrite turns Postgres-style $n placeholders into SQLite's numbered ?n form
// Placeholders inside string literals and quoted identifiers are left untouched
func (d *DB) rewrite(query string) string {
	if d.engine != EngineSQLite || !strings.Contains(query, "$") {
		return query
	}
	if cached, ok := rewriteCache.Load(query); ok {
		return cached.(string)
	}

	var b strings.Builder
	b.Grow(len(query))
	var quote byte
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			b.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			b.WriteByte(c)
		case c == '$' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9':
			b.WriteByte('?')
		default:
			b.WriteByte(c)
		}
	}

	// Statements beyond the cap are rewritten on every call, which is a cheap linear scan
	out := b.String()
	if rewriteCacheSize.Add(1) > maxRewriteCache {
		rewriteCacheSize.Add(-1)
		return out
	}
	if _, loaded := rewriteCache.LoadOrStore(query, out); loaded {
		rewriteCacheSize.Add(-1)
	}
	return out
}

// Now returns the current time as unix milliseconds, the timestamp format of every table
func Now() int64 {
	return time.Now().UnixMilli()
}

// NewID returns a new sortable UUIDv7 string
func NewID() string {
	return uuid.NewV7().String()
}

// IsUniqueViolation reports whether err is a unique constraint violation on either engine
func IsUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505"
	}
	if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok {
		code := sqliteErr.Code()
		return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// IsNotFound reports whether err means a single-row query found nothing
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
