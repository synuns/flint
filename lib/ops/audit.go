// the audit command — the audit trail (Ruling: making revert a command was dropped; flint audit created).
// `flint audit [--limit N] [--session ID] [--stat] [--telemetry]` — the most recent N of dolt_log
// (default 20, cap 100) with only the 4 columns (commit_hash, committer, date, message) as
// one-line JSON {"log":[...]}. --session ID is a message LIKE '%[session:ID]%' filter.
// The default query excludes telemetry commits (messages with the 'telemetry: ' prefix —
// the CommitTelemetry contract) via NOT LIKE; the --telemetry flag includes them.
// Server down exits 2 (silent, §13).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// audit default/cap limits and the usage line.
const (
	auditDefaultLimit = 20
	auditMaxLimit     = 100
	auditUsage        = "usage: flint audit [--limit N] [--session ID] [--stat] [--telemetry]"
)

// auditEntry — the minimal audit output columns (the token-budget ruling: only 4).
// The --stat-only appendix fields (stat, date_rel) are omitempty — the default output keys are unchanged.
type auditEntry struct {
	CommitHash string                    `json:"commit_hash"`
	Committer  string                    `json:"committer"`
	Date       string                    `json:"date"`
	Message    string                    `json:"message"`
	Stat       map[string]auditTableStat `json:"stat,omitempty"`
	DateRel    string                    `json:"date_rel,omitempty"`
}

// auditTableStat — the per-table change summary from dolt_diff (the --stat appendix).
type auditTableStat struct {
	DataChange   bool `json:"data_change"`
	SchemaChange bool `json:"schema_change"`
}

// RelTime — t relative to now ("3h ago", "2d ago" — no log-time option,
// so its own format, C13). Under 1 hour, minutes; under 24 hours, hours; beyond, days (floored).
// Future times are clamped to 0.
func RelTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// RunAudit — the most recent N of dolt_log. limit: 0 or negative -> default 20; capped at 100
// (the invalid-value default-fallback global constraint). The argument-validation paths
// (unknown flag, missing value, non-numeric value) are invalid-arg exit 3 — a non-numeric
// --limit follows the same rule as read and brief (--limit Atoi failure exits 3). Only
// numeric 0/negative get the silent clamp fallback (the read ClampLimit convention).
func RunAudit(st wire.Store, args []string, stdout io.Writer) int {
	ctx := context.Background()
	// positional args not allowed (the wire.ParseArgs spec table).
	// Value validation reproduces occurrence order (AllOccs) — the existing error precedence as-is.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--limit", Value: true},
			{Name: "--stat"},
			{Name: "--session", Value: true},
			{Name: "--telemetry"},
		},
		Usage: auditUsage,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	limit := auditDefaultLimit
	session := res.Value("--session")
	stat := res.Given("--stat")
	telemetry := res.Given("--telemetry")
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--limit":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--limit requires a number — "+auditUsage)))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("invalid limit %q — "+auditUsage, o.Value)))
				return 3
			}
			if n < 1 {
				n = auditDefaultLimit // numeric 0/negative fallback (not an error)
			}
			limit = n
		case "--session":
			if !o.HasValue || o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--session requires an id — "+auditUsage)))
				return 3
			}
		}
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}

	// v1.7.2 T2 (-> v1.8.0 T8 Store migration): by default, telemetry commits (the 'telemetry: '
	// prefix — the CommitTelemetry contract) are excluded via MessageNotLike; the --telemetry
	// flag includes them. The --session filter combines via AND with MessageLike (the
	// '%[session:ID]%' pattern) — all three conditions are passed to the DB side (dialect SQL is lib/db).
	q := wire.LogQuery{LatestN: limit}
	if !telemetry {
		q.MessageNotLike = `telemetry: %`
	}
	if session != "" {
		// a session ID containing LIKE metacharacters (% _) is matched only literally —
		// escaped with query.EscapeLike (the value itself stays parameterized).
		q.MessageLike = `%[session:` + query.EscapeLike(session) + `]%`
	}
	entries, err := st.LogEntries(ctx, q)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	log := make([]auditEntry, 0, len(entries)) // even 0 rows guarantee "log":[]
	for _, en := range entries {
		e := auditEntry{}
		// every LogEntry string field is re-sanitized via render.Text —
		// not only message; hash and committer are also raw-exposure paths.
		e.CommitHash = render.Text(en.Hash)
		e.Committer = render.Text(en.Committer)
		if !en.Date.IsZero() {
			e.Date = fmt.Sprint(en.Date)
		}
		e.Message = render.Text(en.Message) // via render.Text (inheriting re-sanitization of the engine commit-log message)
		if stat && !en.Date.IsZero() {
			e.DateRel = RelTime(en.Date)
		}
		log = append(log, e)
	}
	if stat && len(log) > 0 {
		// the per-table data_change/schema_change summary appendix — instead of asking
		// per commit (an N+1 of up to 100 commits = 100 round trips), a single batch lookup
		// of the hash list (DiffStat — dialect SQL is lib/db).
		hashes := make([]string, 0, len(log))
		for i := range log {
			if log[i].CommitHash != "" {
				hashes = append(hashes, log[i].CommitHash)
			}
		}
		diffStats, err := st.DiffStat(ctx, hashes)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		stats := make(map[string]map[string]auditTableStat, len(log))
		for _, ds := range diffStats {
			// DiffStat's CommitHash and Table are also sanitized —
			// the stats map keys must match the sanitized log-side CommitHash, so they pass
			// through the same outlet (render.Text) for consistency.
			h := render.Text(ds.CommitHash)
			ts := auditTableStat{DataChange: ds.DataChange, SchemaChange: ds.SchemaChange}
			if stats[h] == nil {
				stats[h] = map[string]auditTableStat{}
			}
			stats[h][render.Text(ds.Table)] = ts
		}
		for i := range log {
			if s, ok := stats[log[i].CommitHash]; ok {
				log[i].Stat = s
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"log": log})
	fmt.Fprintln(stdout, string(b))
	return 0
}

// runAudit — the registry handler wrapper (the approve.go pattern — RunAudit does not use
// ctx or stdin).
func runAudit(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return RunAudit(st, args, stdout)
}
