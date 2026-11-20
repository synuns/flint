// history.go — dolt_log history lookups (the Doltgres implementation
// of the wire.LogQuery contract). The single source of commit-log
// access — asof (lib/wire/asof.go)·audit (lib/ops/audit.go) are the
// porting sources. The ported queries are verbatim (the byte-equivalence
// principle). Column-filling scope — LatestN inherits the original 4
// columns (commit_hash·committer·date·message), Hash is a new 4-column
// assembly (replacing the old date 1-column query), Prefix·BeforeOrAt
// fill commit_hash only, Earliest fills date only.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// LogEntries — the dolt_log query per q mode. Mode priority: Earliest >
// Prefix > Hash > BeforeOrAt (non-zero) > LatestN. None specified is an
// error. A query failure retries once (spec §7 — wire.QueryOnce
// equivalent, queryRetry). Prefix returns the full LIKE-match list with
// the uniqueness verdict left to the caller (asof contract inherited);
// BeforeOrAt returns the newest 1 row at or before the time; Earliest
// returns the first commit time as 1 row (date only filled); LatestN is
// DESC recent N with the MessageNotLike NOT LIKE filter (the audit
// default telemetry-exclusion filter expression).
func (r *Repo) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Earliest:
		return r.logEarliest(ctx)
	case q.Prefix != "":
		// Original: the asof.go prefix-resolution query (the full match
		// list).
		return r.logHashRows(ctx,
			`SELECT commit_hash FROM dolt_log WHERE commit_hash LIKE $1||'%' ORDER BY commit_order DESC`,
			q.Prefix)
	case q.Hash != "":
		// Hash mode is a new 4-column (commit_hash·committer·date·
		// message) assembly — replacing the old read/status date
		// 1-column query (values equivalent, assembly differs). Note it
		// is NOT an "original inheritance".
		return r.logFullRows(ctx, `SELECT commit_hash, committer, date, message FROM dolt_log WHERE commit_hash = $1`, q.Hash)
	case !q.BeforeOrAt.IsZero():
		// Original: the asof.go at-or-before adoption query. The date
		// comparison passes a Go-computed time.Time as a timestamptz
		// parameter (inheriting the no-SQL-interval principle).
		return r.logHashRows(ctx,
			`SELECT commit_hash FROM dolt_log WHERE date <= $1 ORDER BY commit_order DESC LIMIT 1`,
			q.BeforeOrAt)
	case q.LatestN > 0:
		return r.logLatestN(ctx, q)
	}
	return nil, fmt.Errorf("log entries: empty query")
}

// logHashRows — a commit_hash single-column query (Prefix·BeforeOrAt
// original inheritance).
func (r *Repo) logHashRows(ctx context.Context, sql string, arg any) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx, sql, arg)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		out = append(out, wire.LogEntry{Hash: h})
	}
	return out, rows.Err()
}

// logFullRows — a 4-column (commit_hash·committer·date·message) query
// (Hash mode). The date cell is parsed by parseLogDate (3 string
// layouts when not a time.Time).
func (r *Repo) logFullRows(ctx context.Context, sql string, arg ...any) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx, sql, arg...)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var e wire.LogEntry
		var d any
		if err := rows.Scan(&e.Hash, &e.Committer, &d, &e.Message); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		t, ok := parseLogDate(d)
		if !ok {
			return nil, fmt.Errorf("log entries: unparseable date %v", d)
		}
		e.Date = t
		out = append(out, e)
	}
	return out, rows.Err()
}

// logEarliest — the first commit time as 1 row. Original: the asof.go
// Earliest query (date-only selection — the commit_hash column left
// unfilled inherits the original query).
func (r *Repo) logEarliest(ctx context.Context) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx,
		`SELECT date FROM dolt_log ORDER BY commit_order ASC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var d any
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		t, ok := parseLogDate(d)
		if !ok {
			return nil, fmt.Errorf("log entries: unparseable date %v", d)
		}
		out = append(out, wire.LogEntry{Date: t})
	}
	return out, rows.Err()
}

// logLatestN — the recent-N lookup (ported from audit.go). Preserves
// the original assembly order: conditions in NOT LIKE (default
// telemetry exclusion)→LIKE (--session inclusion) order, placeholders
// numbered sequentially from $2, AND-joined, LIMIT $1 unchanged at the
// tail.
func (r *Repo) logLatestN(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	sql := `SELECT commit_hash, committer, date, message FROM dolt_log`
	qargs := []any{q.LatestN}
	var conds []string
	ph := 2
	if q.MessageNotLike != "" {
		conds = append(conds, fmt.Sprintf(`message NOT LIKE $%d`, ph))
		qargs = append(qargs, q.MessageNotLike)
		ph++
	}
	if q.MessageLike != "" {
		conds = append(conds, fmt.Sprintf(`message LIKE $%d`, ph))
		qargs = append(qargs, q.MessageLike)
	}
	if len(conds) > 0 {
		sql += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	sql += ` ORDER BY commit_order DESC LIMIT $1`
	return r.logFullRows(ctx, sql, qargs...)
}

// parseLogDate — a dolt_log date value into a time.Time (the real
// implementation yields time.Time; fakes/strings parse across 3
// layouts). Logic transferred from audit.go parseAuditDate (original
// logic preserved).
func parseLogDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, lay := range []string{
			"2006-01-02 15:04:05.999999999 -0700 MST", // time.Time default Sprint
			"2006-01-02 15:04:05.999999999",
			time.RFC3339,
		} {
			if ts, err := time.Parse(lay, x); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}
