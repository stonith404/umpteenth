// Package listquery builds server-side paginated, sorted, searched and filtered queries from a per-endpoint whitelist
package listquery

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
	maxSortKeys     = 3
	// maxPage keeps the OFFSET far from integer overflow, which would turn it negative and make Postgres reject the query
	maxPage = 100_000
)

// Params are the engine-independent list parameters
type Params struct {
	Page     int
	PageSize int
	Sort     string
	Search   string
}

// Spec is the whitelist of one list endpoint
type Spec struct {
	// Select is the SELECT list including FROM and JOINs, without WHERE
	Select string
	// From is the FROM clause including JOINs, used for the count query
	From string
	// CountFrom optionally replaces From for counting, leaving out joins that only add display columns
	// It must still contain every table that filters and search reference
	CountFrom string
	// Key optionally names the unique row key, which makes the page query pick the page's keys first and load full rows only for them
	// Large tables need this, because sorting on a column without an index otherwise sorts every full row before the LIMIT applies
	Key string
	// JoinSorts lists the sort keys whose expressions need a table that only From joins
	// With Key and CountFrom set, keys are picked over CountFrom for every other sort, which skips the join for each scanned row
	JoinSorts []string
	// Sorts maps public sort keys to SQL expressions
	Sorts map[string]string
	// NullableSorts lists the sort keys whose expressions can be NULL, which sort last in both directions
	// SQLite and Postgres place NULLs at opposite ends by default, so without this the engines return different pages
	// Keys that are never NULL must not be listed, because NULLS LAST stops Postgres from walking a plain index in descending order
	NullableSorts []string
	// DefaultSort is used when the request has no sort, e.g. "-created_at"
	DefaultSort string
	// Search lists the SQL expressions matched by the free-text search
	Search []string
	// SearchConds lists extra search conditions in which every {} stands for the lowercased LIKE pattern
	// A subquery here searches a joined table's column without making the planner scan through the join
	SearchConds []string
	// TieBreaker is appended to every ORDER BY so pages are stable, typically the primary key
	TieBreaker string
}

// Builder accumulates WHERE conditions and their arguments
type Builder struct {
	spec  *Spec
	where []string
	args  []any
}

// New starts a query for the given spec
func New(spec *Spec) *Builder {
	return &Builder{spec: spec}
}

// Arg adds an argument and returns its Postgres-style placeholder, which the database layer rewrites for SQLite
func (b *Builder) Arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

// Where adds a condition built with placeholders from Arg
func (b *Builder) Where(cond string) *Builder {
	b.where = append(b.where, cond)
	return b
}

// WhereEq adds "expr = value"
func (b *Builder) WhereEq(expr string, value any) *Builder {
	return b.Where(expr + " = " + b.Arg(value))
}

// WhereIn adds "expr IN (...)" for a non-empty set of values, ignoring empty sets
func (b *Builder) WhereIn(expr string, values []string) *Builder {
	if len(values) == 0 {
		return b
	}
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = b.Arg(v)
	}
	return b.Where(expr + " IN (" + strings.Join(placeholders, ", ") + ")")
}

// Query is a built page query and its count query, each with its own arguments
type Query struct {
	Page      string
	PageArgs  []any
	Count     string
	CountArgs []any
}

// Build returns the page query and the count query
func (b *Builder) Build(p Params) (Query, error) {
	// Free-text search matches any whitelisted column, case-insensitively and portably
	if s := strings.TrimSpace(p.Search); s != "" && len(b.spec.Search)+len(b.spec.SearchConds) > 0 {
		arg := b.Arg("%" + escapeLike(strings.ToLower(s)) + "%")
		conds := make([]string, 0, len(b.spec.Search)+len(b.spec.SearchConds))
		for _, col := range b.spec.Search {
			conds = append(conds, "lower("+col+") LIKE "+arg+" ESCAPE '\\'")
		}
		for _, cond := range b.spec.SearchConds {
			conds = append(conds, strings.ReplaceAll(cond, "{}", arg))
		}
		b.Where("(" + strings.Join(conds, " OR ") + ")")
	}

	where := ""
	if len(b.where) > 0 {
		where = " WHERE " + strings.Join(b.where, " AND ")
	}

	orderBy, needsJoin, err := b.orderBy(p.Sort)
	if err != nil {
		return Query{}, err
	}

	// The count query only sees the filter arguments, since Postgres rejects arguments a statement does not reference
	countFrom := b.spec.From
	if b.spec.CountFrom != "" {
		countFrom = b.spec.CountFrom
	}
	q := Query{Count: "SELECT COUNT(*) " + countFrom + where, CountArgs: slices.Clone(b.args)}

	// LIMIT and OFFSET are bound as arguments, so every page shares one statement text and the statement caches stay small
	page, size := normalizePage(p)
	limit := " ORDER BY " + orderBy + " LIMIT " + b.Arg(size) + " OFFSET " + b.Arg((page-1)*size)
	if b.spec.Key != "" {
		keyFrom := b.spec.From
		if b.spec.CountFrom != "" && !needsJoin {
			keyFrom = b.spec.CountFrom
		}
		q.Page = b.spec.Select + " WHERE " + b.spec.Key + " IN (SELECT " + b.spec.Key + " " + keyFrom + where + limit + ") ORDER BY " + orderBy
	} else {
		q.Page = b.spec.Select + where + limit
	}
	q.PageArgs = b.args
	return q, nil
}

// orderBy returns the ORDER BY list and whether any of its keys needs a join from From
func (b *Builder) orderBy(sort string) (string, bool, error) {
	if strings.TrimSpace(sort) == "" {
		sort = b.spec.DefaultSort
	}

	var parts []string
	needsJoin := false
	for key := range strings.SplitSeq(sort, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		dir := "ASC"
		if strings.HasPrefix(key, "-") {
			dir = "DESC"
			key = key[1:]
		}
		expr, ok := b.spec.Sorts[key]
		if !ok {
			return "", false, apperror.InvalidField("sort", "invalid", "has unknown sort key "+strconv.Quote(key))
		}
		term := expr + " " + dir
		if slices.Contains(b.spec.NullableSorts, key) {
			term += " NULLS LAST"
		}
		parts = append(parts, term)
		if len(parts) > maxSortKeys {
			return "", false, apperror.InvalidField("sort", "invalid", "has too many sort keys")
		}
		needsJoin = needsJoin || slices.Contains(b.spec.JoinSorts, key)
	}
	if b.spec.TieBreaker != "" {
		parts = append(parts, b.spec.TieBreaker+" DESC")
	}
	return strings.Join(parts, ", "), needsJoin, nil
}

func normalizePage(p Params) (page int, size int) {
	page, size = p.Page, p.PageSize
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Querier is what Run needs from the database layer
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Run executes the page and count queries and scans each row with scan
func Run[T any](ctx context.Context, db Querier, b *Builder, p Params, scan func(rows *sql.Rows) (T, error)) ([]T, int64, error) {
	q, err := b.Build(p)
	if err != nil {
		return nil, 0, err
	}

	var total int64
	err = db.QueryRowContext(ctx, q.Count, q.CountArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count rows: %w", err)
	}

	rows, err := db.QueryContext(ctx, q.Page, q.PageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query rows: %w", err)
	}
	defer rows.Close()

	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to iterate rows: %w", err)
	}

	return items, total, nil
}

// SplitCSV splits a comma-separated filter value into trimmed, non-empty values
func SplitCSV(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
