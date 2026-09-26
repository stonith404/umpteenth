//go:build unit

package listquery

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

var spec = &Spec{
	Select:      "SELECT id FROM things t",
	From:        "FROM things t",
	Sorts:       map[string]string{"name": "t.name", "createdAt": "t.created_at"},
	DefaultSort: "-createdAt",
	Search:      []string{"t.name", "t.description"},
	TieBreaker:  "t.id",
}

func TestBuildAppliesFiltersSortSearchAndPaging(t *testing.T) {
	q := New(spec).WhereEq("t.workspace_id", "ws1")
	q.WhereIn("t.status", []string{"a", "b"})
	built, err := q.Build(Params{Page: 3, PageSize: 10, Sort: "name,-createdAt", Search: "50%_off"})
	require.NoError(t, err)

	require.Equal(t, "SELECT id FROM things t WHERE t.workspace_id = $1 AND t.status IN ($2, $3) AND (lower(t.name) LIKE $4 ESCAPE '\\' OR lower(t.description) LIKE $4 ESCAPE '\\') ORDER BY t.name ASC, t.created_at DESC, t.id DESC LIMIT $5 OFFSET $6", built.Page)
	require.Equal(t, "SELECT COUNT(*) FROM things t WHERE t.workspace_id = $1 AND t.status IN ($2, $3) AND (lower(t.name) LIKE $4 ESCAPE '\\' OR lower(t.description) LIKE $4 ESCAPE '\\')", built.Count)
	// LIKE wildcards in the search text are escaped, so they match literally
	require.Equal(t, []any{"ws1", "a", "b", `%50\%\_off%`, 10, 20}, built.PageArgs)
	// The count query gets only the filter arguments, because Postgres rejects unreferenced ones
	require.Equal(t, []any{"ws1", "a", "b", `%50\%\_off%`}, built.CountArgs)
}

func TestBuildUsesDefaultsAndClampsPaging(t *testing.T) {
	built, err := New(spec).Build(Params{Page: 0, PageSize: 5000})
	require.NoError(t, err)
	require.Contains(t, built.Page, "ORDER BY t.created_at DESC, t.id DESC LIMIT $1 OFFSET $2")
	require.Equal(t, []any{100, 0}, built.PageArgs)

	// A huge page number is clamped, so the offset can never overflow into a negative value
	built, err = New(spec).Build(Params{Page: math.MaxInt, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, []any{100, (maxPage - 1) * 100}, built.PageArgs)
}

func TestBuildSharesOneStatementAcrossPages(t *testing.T) {
	// Paging must not change the statement text, since the SQLite placeholder rewrite and pgx cache one entry per distinct statement
	first, err := New(spec).Build(Params{Page: 1})
	require.NoError(t, err)
	later, err := New(spec).Build(Params{Page: 9876, PageSize: 7})
	require.NoError(t, err)
	require.Equal(t, first.Page, later.Page)
	require.Equal(t, first.Count, later.Count)
}

func TestBuildPutsNullsLastOnlyForNullableSorts(t *testing.T) {
	nullable := *spec
	nullable.Sorts = map[string]string{"name": "t.name", "finishedAt": "t.finished_at"}
	nullable.NullableSorts = []string{"finishedAt"}

	built, err := New(&nullable).Build(Params{Sort: "-finishedAt,name"})
	require.NoError(t, err)
	require.Contains(t, built.Page, "ORDER BY t.finished_at DESC NULLS LAST, t.name ASC, t.id DESC LIMIT")

	built, err = New(&nullable).Build(Params{Sort: "finishedAt"})
	require.NoError(t, err)
	require.Contains(t, built.Page, "ORDER BY t.finished_at ASC NULLS LAST, t.id DESC LIMIT")
}

func TestBuildRejectsUnknownSortKeys(t *testing.T) {
	_, err := New(spec).Build(Params{Sort: "password"})
	require.True(t, apperror.IsCode(err, apperror.CodeValidationFailed))

	// Injection attempts are just unknown keys
	_, err = New(spec).Build(Params{Sort: "name; DROP TABLE things"})
	require.Error(t, err)
}

func TestEmptyInFilterIsIgnored(t *testing.T) {
	built, err := New(spec).WhereIn("t.status", nil).Build(Params{})
	require.NoError(t, err)
	require.NotContains(t, built.Page, "WHERE")
	require.Empty(t, built.CountArgs)
}

func TestBuildWithKeyPagesOverKeysFirst(t *testing.T) {
	keyed := *spec
	keyed.Select = "SELECT t.id, o.name FROM things t JOIN owners o ON o.id = t.owner_id"
	keyed.From = "FROM things t JOIN owners o ON o.id = t.owner_id"
	keyed.CountFrom = "FROM things t"
	keyed.Key = "t.id"
	keyed.Sorts = map[string]string{"name": "t.name", "owner": "o.name", "createdAt": "t.created_at"}
	keyed.JoinSorts = []string{"owner"}

	built, err := New(&keyed).WhereEq("t.workspace_id", "ws1").Build(Params{Page: 2, PageSize: 10, Sort: "name"})
	require.NoError(t, err)
	require.Equal(t, "SELECT t.id, o.name FROM things t JOIN owners o ON o.id = t.owner_id WHERE t.id IN (SELECT t.id FROM things t WHERE t.workspace_id = $1 ORDER BY t.name ASC, t.id DESC LIMIT $2 OFFSET $3) ORDER BY t.name ASC, t.id DESC", built.Page)
	require.Equal(t, []any{"ws1", 10, 10}, built.PageArgs)
	require.Equal(t, "SELECT COUNT(*) FROM things t WHERE t.workspace_id = $1", built.Count)

	// Sorting by a joined column picks the keys over the full FROM clause
	built, err = New(&keyed).Build(Params{Sort: "-owner"})
	require.NoError(t, err)
	require.Contains(t, built.Page, "IN (SELECT t.id FROM things t JOIN owners o ON o.id = t.owner_id ORDER BY o.name DESC, t.id DESC LIMIT")

	// Search conditions get the same pattern placeholder as the plain search columns
	keyed.SearchConds = []string{"t.owner_id IN (SELECT id FROM owners WHERE lower(name) LIKE {} ESCAPE '\\')"}
	built, err = New(&keyed).Build(Params{Search: "Bob"})
	require.NoError(t, err)
	require.Equal(t, "SELECT COUNT(*) FROM things t WHERE (lower(t.name) LIKE $1 ESCAPE '\\' OR lower(t.description) LIKE $1 ESCAPE '\\' OR t.owner_id IN (SELECT id FROM owners WHERE lower(name) LIKE $1 ESCAPE '\\'))", built.Count)
	require.Equal(t, []any{"%bob%"}, built.CountArgs)
}

func TestRunPagesOnBothEngines(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	ctx := context.Background()

	// The tables get unique names and are dropped afterwards, so the test also works on a database shared between runs
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	owners, things := "lq_owners_"+suffix, "lq_things_"+suffix
	testutil.Exec(t, db, "CREATE TABLE "+owners+" (id TEXT PRIMARY KEY, name TEXT NOT NULL)")
	testutil.Exec(t, db, "CREATE TABLE "+things+" (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL, finished_at BIGINT)")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TABLE "+things)
		_, _ = db.ExecContext(context.Background(), "DROP TABLE "+owners)
	})
	testutil.Exec(t, db, "INSERT INTO "+owners+" (id, name) VALUES ('o1', 'Alice')")

	// Five things, two of them unfinished, so the nullable sort key has NULLs to place
	for i, finished := range []*int64{new(int64(30)), nil, new(int64(10)), nil, new(int64(20))} {
		testutil.Exec(t, db, "INSERT INTO "+things+" (id, owner_id, name, description, finished_at) VALUES ($1, 'o1', $2, '', $3)", fmt.Sprintf("t%d", i), fmt.Sprintf("thing %d", i), finished)
	}

	keyed := &Spec{
		Select:        "SELECT t.id FROM " + things + " t JOIN " + owners + " o ON o.id = t.owner_id",
		From:          "FROM " + things + " t JOIN " + owners + " o ON o.id = t.owner_id",
		CountFrom:     "FROM " + things + " t",
		Key:           "t.id",
		Sorts:         map[string]string{"name": "t.name", "finishedAt": "t.finished_at"},
		NullableSorts: []string{"finishedAt"},
		DefaultSort:   "name",
		TieBreaker:    "t.id",
	}
	scan := func(rows *sql.Rows) (string, error) {
		var id string
		return id, rows.Scan(&id)
	}

	// The bound LIMIT and OFFSET work inside the key subquery, and the count ignores them
	ids, total, err := Run(ctx, db, New(keyed).WhereIn("t.owner_id", []string{"o1"}), Params{Page: 2, PageSize: 2}, scan)
	require.NoError(t, err)
	require.Equal(t, int64(5), total)
	require.Equal(t, []string{"t2", "t3"}, ids)

	// Unfinished things sort last in both directions on both engines
	ids, _, err = Run(ctx, db, New(keyed), Params{PageSize: 10, Sort: "-finishedAt"}, scan)
	require.NoError(t, err)
	require.Equal(t, []string{"t0", "t4", "t2", "t3", "t1"}, ids)
	ids, _, err = Run(ctx, db, New(keyed), Params{PageSize: 10, Sort: "finishedAt"}, scan)
	require.NoError(t, err)
	require.Equal(t, []string{"t2", "t4", "t0", "t3", "t1"}, ids)

	// A page far past the end is empty rather than an error
	ids, _, err = Run(ctx, db, New(keyed), Params{Page: 1 << 62, PageSize: 100}, scan)
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestSplitCSV(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, SplitCSV(" a, ,b,"))
	require.Nil(t, SplitCSV(""))
}
