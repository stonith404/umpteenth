//go:build unit

package database

import (
	"fmt"
	"strings"
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

func TestRewriteSkipsCachingHugeStatements(t *testing.T) {
	d := &DB{engine: EngineSQLite}

	// The cache starts empty, so the statement is left out for its size and not because earlier tests filled the cache
	rewriteCache.Clear()
	rewriteCacheSize.Store(0)
	t.Cleanup(func() {
		rewriteCache.Clear()
		rewriteCacheSize.Store(0)
	})

	// A list filter with thousands of values in the URL builds a statement like this one, which must not be remembered
	placeholders := make([]string, 5000)
	want := make([]string, len(placeholders))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		want[i] = fmt.Sprintf("?%d", i+1)
	}
	query := "SELECT * FROM t WHERE a IN (" + strings.Join(placeholders, ", ") + ")"
	require.Greater(t, len(query), maxCachedStatement)

	require.Equal(t, "SELECT * FROM t WHERE a IN ("+strings.Join(want, ", ")+")", d.rewrite(query))
	_, cached := rewriteCache.Load(query)
	require.False(t, cached)
}
