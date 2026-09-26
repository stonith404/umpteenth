package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/database"
)

type databaseStorage struct {
	db *database.DB
}

// NewDatabaseStorage stores blobs in the blobs table, which works for HA without extra infrastructure
func NewDatabaseStorage(db *database.DB) FileStorage {
	return &databaseStorage{db: db}
}

func (s *databaseStorage) Save(ctx context.Context, key string, data io.Reader) error {
	buf, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("failed to read blob: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO blobs (key, data, size, updated_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET data = excluded.data, size = excluded.size, updated_at = excluded.updated_at`,
		cleanKey(key), buf, int64(len(buf)), database.Now())
	if err != nil {
		return fmt.Errorf("failed to save blob: %w", err)
	}
	return nil
}

func (s *databaseStorage) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM blobs WHERE key = $1", cleanKey(key)).Scan(&data)
	if database.IsNotFound(err) {
		return nil, 0, ErrNotExist
	} else if err != nil {
		return nil, 0, fmt.Errorf("failed to load blob: %w", err)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (s *databaseStorage) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM blobs WHERE key = $1", cleanKey(key))
	if err != nil {
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	return nil
}

func (s *databaseStorage) DeleteAll(ctx context.Context, prefix string) error {
	cond, args := s.prefixCondition(prefix)
	_, err := s.db.ExecContext(ctx, "DELETE FROM blobs WHERE "+cond, args...)
	if err != nil {
		return fmt.Errorf("failed to delete blobs: %w", err)
	}
	return nil
}

func (s *databaseStorage) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	cond, args := s.prefixCondition(prefix)
	rows, err := s.db.QueryContext(ctx, "SELECT key, size, updated_at FROM blobs WHERE "+cond+" ORDER BY key", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list blobs: %w", err)
	}
	defer rows.Close()

	var out []ObjectInfo
	for rows.Next() {
		var (
			info      ObjectInfo
			updatedAt int64
		)
		err = rows.Scan(&info.Key, &info.Size, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan blob: %w", err)
		}
		info.ModTime = time.UnixMilli(updatedAt)
		out = append(out, info)
	}
	return out, rows.Err()
}

func (s *databaseStorage) Close() error { return nil }

// prefixCondition returns a condition on key matching every blob below the directory-like prefix, and its arguments
// SQLite compares text bytewise, so a key range there walks the primary key index instead of running LIKE over every blob
// Postgres compares text in the database collation, where a key range is not the same as a byte prefix, so it keeps the escaped LIKE
func (s *databaseStorage) prefixCondition(prefix string) (string, []any) {
	prefix = dirPrefix(prefix)
	if s.db.Engine() != database.EngineSQLite {
		return `key LIKE $1 ESCAPE '\'`, []any{likePrefix(prefix)}
	}
	upper, ok := prefixUpperBound(prefix)
	if !ok {
		return "key >= $1", []any{prefix}
	}
	return "key >= $1 AND key < $2", []any{prefix, upper}
}

// prefixUpperBound returns the smallest string that sorts bytewise after every string starting with prefix
// It reports false when no such string exists, which happens for an empty prefix or one made only of 0xFF bytes
func prefixUpperBound(prefix string) (string, bool) {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

func likePrefix(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
}
