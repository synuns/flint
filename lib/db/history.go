package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func (r *Repo) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Earliest:
		return r.logEarliest(ctx)
	case q.Prefix != "":

		return r.logFullRows(ctx,
			`SELECT commit_hash, committer, date, message FROM dolt_log WHERE commit_hash LIKE $1||'%' ORDER BY commit_order DESC`,
			q.Prefix)
	case q.Hash != "":

		return r.logFullRows(ctx, `SELECT commit_hash, committer, date, message FROM dolt_log WHERE commit_hash = $1`, q.Hash)
	case !q.BeforeOrAt.IsZero():

		return r.logFullRows(ctx,
			`SELECT commit_hash, committer, date, message FROM dolt_log WHERE date <= $1 ORDER BY commit_order DESC LIMIT 1`,
			q.BeforeOrAt)
	case q.LatestN > 0:
		return r.logLatestN(ctx, q)
	}
	return nil, fmt.Errorf("log entries: empty query")
}

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

func parseLogDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, lay := range []string{
			"2006-01-02 15:04:05.999999999 -0700 MST",
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
