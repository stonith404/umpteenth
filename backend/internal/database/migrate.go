package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/stonith404/umpteenth/backend/resources"
)

// Migrate applies all pending migrations for the handle's engine
// On Postgres a session advisory lock serializes replicas that start at the same time
func Migrate(ctx context.Context, db *DB) error {
	provider, err := migrationProvider(db)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "Applied database migration", slog.String("migration", r.Source.Path), slog.Duration("duration", r.Duration))
	}

	return nil
}

// migrationProvider runs the embedded migrations of the handle's engine
func migrationProvider(db *DB) (*goose.Provider, error) {
	var (
		dialect goose.Dialect
		dir     string
		opts    []goose.ProviderOption
	)
	switch db.engine {
	case EngineSQLite:
		dialect = goose.DialectSQLite3
		dir = "migrations/sqlite"
	case EnginePostgres:
		dialect = goose.DialectPostgres
		dir = "migrations/postgres"
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf("failed to create migration locker: %w", err)
		}
		opts = append(opts, goose.WithSessionLocker(locker))
	}

	fsys, err := fs.Sub(resources.Migrations, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(dialect, db.raw, fsys, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create migration provider: %w", err)
	}
	return provider, nil
}
