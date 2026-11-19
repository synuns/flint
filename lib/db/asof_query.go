// asof_query.go — the AS OF point-in-time query (the Doltgres
// implementation of the wire.AsOfQuery contract). The caller
// (lib/knowledge read) no longer assembles a raw AS OF clause —
// inlining·hash-charset revalidation·view-incompatibility guidance
// wrapping are this layer's single source. Assembly shape:
// "SELECT {Columns} FROM {Table} AS OF '{hash}' {Where} LIMIT {Limit}"
// (Limit is an int literal; 0 or below omits the LIMIT clause).
package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/lib/wire"
)

// asOfTables — the tables allowed for AS OF queries. The base
// (knowledge) and the view (active_knowledge) only — other references
// are rejected before assembly.
var asOfTables = map[string]bool{
	"knowledge":        true,
	"active_knowledge": true,
}

// asOfHashReDB — the strict charset of the AS OF inline hash (lowercase
// base32-family, the same rule as lib/knowledge asOfHashRe — the
// original must not be modified, so an equivalent definition was newly
// written in db). A double check of the engine-returned value: a
// violation is an error instead of inlining.
var asOfHashReDB = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

// asOfColumnRe — Columns identifier validation. Lowercase·digits·
// underscore·parens·asterisk·space·comma only (functions·expressions
// allowed — count(*)·arithmetic). Uppercase·sentence punctuation·dash
// rejected.
var asOfColumnRe = regexp.MustCompile(`^[a-z_0-9)(* ,]+$`)

// asOfWhereKeywordRe — decides whether Where already starts with a
// clause keyword (where/order/limit, case-insensitive). The read path's
// tail-clause form is attached as-is; the status path's conditions-only
// form gets the " WHERE " prefix attached.
var asOfWhereKeywordRe = regexp.MustCompile(`(?i)^(where|order|limit)\b`)

// QueryAsOf — assembles q and runs the point-in-time query. Where
// accepts, as a trust boundary receiving only repo-internal query-
// assembler output, either the raw tail clause after the table
// reference ("WHERE ... ORDER BY ..." — $N binds positionally to Args)
// or the conditions-only form ("status = 'pending' AND ..." — the
// assembler attaches the WHERE prefix) (external input is blocked by
// the Columns·Hash·Table validation). On view (active_knowledge)
// incompatibility — "column ... does not exist" (current definition +
// past rows, C3) — immediately returns the guidance-wrapped error
// without a base retry (the guidance text is equivalent to the original
// read.go wrapViewColumnMismatch — the isViewColumnMismatch verdict
// includes the "references invalid table(s)" extension over the
// original heuristic. The base lacks the invalid_at filter, so fallback
// exposure breaks point-in-time consistency).
func (r *Repo) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	rows, err := r.queryAsOfTable(ctx, q, q.Table)
	if err != nil {
		if isViewColumnMismatch(err) {
			// Original preserved (transferred from read.go
			// wrapViewColumnMismatch) — guidance text kept.
			return nil, fmt.Errorf("%w — the active_knowledge view definition is incompatible with rows at that point in time; try an AS OF query on the base table (knowledge)", err)
		}
		return nil, err
	}
	return rows, nil
}

// queryAsOfTable — validates then runs the single-table AS OF assembly.
func (r *Repo) queryAsOfTable(ctx context.Context, q wire.AsOfQuery, table string) ([][]any, error) {
	if !asOfTables[table] {
		return nil, fmt.Errorf("query as of: table %q not allowed (knowledge, active_knowledge)", table)
	}
	if !asOfHashReDB.MatchString(q.Hash) {
		// Original-aligned (read.go) — "as-of hash charset violation".
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
		// Both Where forms accepted: read (T7) passes a tail clause
		// starting with a keyword ("WHERE ... ORDER BY ..."); status
		// (T8) passes conditions only ("status = 'pending' AND ...").
		// When it does not start with a leading clause keyword
		// (where/order/limit, case-insensitive), the " WHERE " prefix
		// is attached — keyword-form keeps byte equivalence.
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

// isViewColumnMismatch — the view-column incompatibility verdict (a
// column absent from past rows). Inherits the original read.go
// wrapViewColumnMismatch heuristic + extends it with the measured
// Doltgres view-AS OF shape ("references invalid table(s) or
// column(s)") — the original heuristic alone misses this shape
// (measured).
func isViewColumnMismatch(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "column") && strings.Contains(msg, "does not exist") {
		return true
	}
	return strings.Contains(msg, "references invalid table(s) or column(s)")
}

// queryRows — a pgx query into [][]any (the pool Query common pattern —
// rows.Values). A query failure retries once (spec §7).
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

// queryRetry — runs pool.Query with one retry (shared by all db history
// reads).
func (r *Repo) queryRetry(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return retryOnce(func() (pgx.Rows, error) {
		return r.pool.Query(ctx, sql, args...)
	})
}

// retryOnce — one read retry (spec §7 — an in-db copy of the
// wire.QueryOnce equivalent contract). First failure → one re-run →
// final result propagated.
func retryOnce[T any](fn func() (T, error)) (T, error) {
	v, err := fn()
	if err != nil {
		v, err = fn()
	}
	return v, err
}
