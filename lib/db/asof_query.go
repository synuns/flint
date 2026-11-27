package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/lib/wire"
)

var asOfTables = map[string]bool{
	"knowledge":        true,
	"active_knowledge": true,
	"recall_knowledge": true,
}

var asOfHashReDB = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

var asOfColumnRe = regexp.MustCompile(`^[a-z_0-9)(* ,]+$`)

var asOfWhereKeywordRe = regexp.MustCompile(`(?i)^(where|order|limit)\b`)

func (r *Repo) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	rows, err := r.queryAsOfTable(ctx, q, q.Table)
	if err != nil {
		if isViewColumnMismatch(err) {

			return nil, fmt.Errorf("%w — the %s view definition is incompatible with rows at that point in time; try an AS OF query on the base table (knowledge)", err, q.Table)
		}
		return nil, err
	}
	return rows, nil
}

func (r *Repo) queryAsOfTable(ctx context.Context, q wire.AsOfQuery, table string) ([][]any, error) {
	if !asOfTables[table] {
		return nil, fmt.Errorf("query as of: table %q not allowed (knowledge, active_knowledge, recall_knowledge)", table)
	}
	if !asOfHashReDB.MatchString(q.Hash) {

		return nil, fmt.Errorf("as-of hash charset violation: %q", q.Hash)
	}
	for _, c := range q.Columns {
		if !asOfColumnRe.MatchString(c) {
			return nil, fmt.Errorf("query as of: invalid column expression %q", c)
		}
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(q.Columns, ", "))
	b.WriteString(" FROM ")
	b.WriteString(table)
	b.WriteString(" AS OF '")
	b.WriteString(q.Hash)
	b.WriteString("'")
	if q.Where != "" {

		b.WriteString(" ")
		if asOfWhereKeywordRe.MatchString(q.Where) {
			b.WriteString(q.Where)
		} else {
			b.WriteString("WHERE ")
			b.WriteString(q.Where)
		}
	}
	if q.Limit > 0 {
		b.WriteString(fmt.Sprintf(" LIMIT %d", q.Limit))
	}
	return r.queryRows(ctx, b.String(), q.Args...)
}

func isViewColumnMismatch(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "column") && strings.Contains(msg, "does not exist") {
		return true
	}
	return strings.Contains(msg, "references invalid table(s) or column(s)")
}

func (r *Repo) queryRows(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := r.queryRetry(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, verr := rows.Values()
		if verr != nil {
			return nil, verr
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func (r *Repo) queryRetry(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return retryOnce(func() (pgx.Rows, error) {
		return r.pool.Query(ctx, sql, args...)
	})
}

func retryOnce[T any](fn func() (T, error)) (T, error) {
	v, err := fn()
	if err != nil {
		v, err = fn()
	}
	return v, err
}
