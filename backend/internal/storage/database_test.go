//go:build unit

package storage

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestDatabaseStorageMatchesKeysByPrefix(t *testing.T) {
	ctx := context.Background()
	s := NewDatabaseStorage(testutil.NewDatabaseForTest(t))

	// Every key lives under a unique root, so blobs of other tests sharing the database never show up
	root := "t" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/"
	listKeys := func(prefix string) []string {
		list, err := s.List(ctx, root+prefix)
		require.NoError(t, err)
		keys := make([]string, len(list))
		for i, o := range list {
			keys[i] = strings.TrimPrefix(o.Key, root)
		}
		// The listing order follows the database collation, so the keys are compared bytewise here
		slices.Sort(keys)
		return keys
	}

	// Neighbouring keys and LIKE wildcards check that the prefix match is exact on both engines
	for _, key := range []string{"runs/a_b/1", "runs/a_b/2", "runs/a_b0/1", "runs/aXb/1", "runs/a_a/1", "runs/a_c", "other/a_b/1"} {
		require.NoError(t, s.Save(ctx, root+key, strings.NewReader("x")))
	}
	require.Equal(t, []string{"runs/a_b/1", "runs/a_b/2", "runs/a_b0/1"}, listKeys("runs/a_b"))

	// Deleting a prefix leaves every other key alone, and a trailing slash is cleaned away like everywhere else
	require.NoError(t, s.DeleteAll(ctx, root+"runs/a_b/"))
	require.Equal(t, []string{"other/a_b/1", "runs/aXb/1", "runs/a_a/1", "runs/a_c"}, listKeys(""))
	require.NoError(t, s.DeleteAll(ctx, root))
	require.Empty(t, listKeys(""))
}

func TestPrefixUpperBound(t *testing.T) {
	upper, ok := prefixUpperBound("runs/abc")
	require.True(t, ok)
	require.Equal(t, "runs/abd", upper)

	// Trailing 0xFF bytes cannot be incremented, so the bound carries into the byte before them
	upper, ok = prefixUpperBound("a\xff\xff")
	require.True(t, ok)
	require.Equal(t, "b", upper)

	_, ok = prefixUpperBound("")
	require.False(t, ok)
	_, ok = prefixUpperBound("\xff")
	require.False(t, ok)
}
