//go:build unit

package database

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewritePlaceholders(t *testing.T) {
	d := &DB{engine: EngineSQLite}

	require.Equal(t, "SELECT * FROM t WHERE a = ?1 AND b = ?12", d.rewrite("SELECT * FROM t WHERE a = $1 AND b = $12"))

	// Literals keep their dollar signs
	require.Equal(t, "SELECT '$1', \"$2\" FROM t WHERE a = ?3", d.rewrite("SELECT '$1', \"$2\" FROM t WHERE a = $3"))

	// Postgres queries are passed through unchanged
	pg := &DB{engine: EnginePostgres}
	require.Equal(t, "SELECT $1", pg.rewrite("SELECT $1"))
}

func TestRewriteCacheIsBounded(t *testing.T) {
	d := &DB{engine: EngineSQLite}

	// Distinct statements past the cap are still rewritten, just no longer remembered
	for i := range maxRewriteCache + 500 {
		query := fmt.Sprintf("SELECT * FROM t WHERE a = $1 LIMIT %d", i)
		require.Equal(t, fmt.Sprintf("SELECT * FROM t WHERE a = ?1 LIMIT %d", i), d.rewrite(query))
	}

	entries := 0
	rewriteCache.Range(func(_, _ any) bool {
		entries++
		return true
	})
	require.LessOrEqual(t, entries, maxRewriteCache)
	require.Equal(t, int64(entries), rewriteCacheSize.Load())
}
