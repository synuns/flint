// status.go — flint status: ping + schema_version + pending count as one-line JSON.
// windowedDaily (cli report.go) and the hot/read metering loaders (cli
// metrics.go) are injected via Fn seams.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// DailyRow — the daily metering columns needed for status's windowed metrics (report windowedDaily
// compatible — the seam-passing type).
type DailyRow struct {
	Read     int
	ReadZero int
	ReadNear int
}

// WindowedDailyFn — the N-day window daily-rows seam. The real implementation (cli report.go
// windowedDaily) is injected by the cli/ops_alias_1.go init as a conversion wrapper.
var WindowedDailyFn func(ctx context.Context, st wire.Store, days int) ([]DailyRow, error)

// ReadZeroPctFn — the zero ratio (integer percent, calls-0 guard) seam (cli metrics.go).
var ReadZeroPctFn func(calls, zero int) int

// HotStatsFn — the cumulative hot metering exposure seam (cli metrics.go).
var HotStatsFn func() (calls int, p95 int, max int)

// LoadHotStatsFromMetaFn — the meta hot_stats baseline load seam (cli metrics.go).
var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

// LoadReadStatsFromMetaFn — the meta read_stats load seam (cli metrics.go).
var LoadReadStatsFromMetaFn func(ctx context.Context, st wire.Store) (calls int, zero int)

// StatusOutput — serialized with key order status, schema_version, pending.
type StatusOutput struct {
	Status        string  `json:"status"`
	SchemaVersion string  `json:"schema_version"`
	Pending       int     `json:"pending"`
	HotCalls      int     `json:"hot_calls"`
	HotP95        int     `json:"hot_p95"`
	ReadCalls     int     `json:"read_calls"`
	ReadZeroPct   int     `json:"read_zero_pct"`
	LastWriteAt   *string `json:"last_write_at"`
	LastHotAt     *string `json:"last_hot_at"`
	LastReadAt    *string `json:"last_read_at"`
}

// statusV096Output — embeds the StatusOutput fields, keeping the key order as-is
// and appending read_fallback_calls (metering meta exposure) at the end. Unset is
// null (inheriting the last_* pattern). StatusOutput itself is unchanged (the existing
// field-and-order contract — covered by the status_v06/v08 reflection tests).
type statusV096Output struct {
	StatusOutput
	ReadFallbackCalls *int `json:"read_fallback_calls"`
}

// statusV0180Output — windowed read metrics added. The cumulative read_calls and
// read_zero_pct are dominated by early history and misread the current state (cases
// where the cumulative and the report's windowed values diverged sharply) — so a 14-day
// daily-sum window value is reported alongside (the same formula as report zero_pct). Existing keys and order unchanged (additive).
type statusV0180Output struct {
	statusV096Output
	ReadCalls14d       int `json:"read_calls_14d"`
	ReadZeroPct14d     int `json:"read_zero_pct_14d"`
	ReadZeroNearPct14d int `json:"read_zero_near_pct_14d"`
	// the *_14d name is kept for backward compatibility; the actual window is expressed by this field
	// (reflecting the daily_retention_days override — false-name defense).
	ReadWindowDays int `json:"read_window_days"`
	// G5 minimal injection — the windowed sum of hot (injection) and read output bytes
	// (monitoring actual compliance with hot_budget, METRICS §4 G5). Additive, appended at the end.
	HotBytes14d  int `json:"hot_bytes_14d"`
	ReadBytes14d int `json:"read_bytes_14d"`
	// per-surface bytes p95 (spec §2.3). read uses the read_events bytes
	// distribution (nearest-rank; <5 samples is 0); hot derives from hot_events x summary lengths
	// (DerivedHotStats). Additive, appended at the end.
	ReadBytesP95 int `json:"read_bytes_p95"`
	HotBytesP95  int `json:"hot_bytes_p95"`
}

// StatusWindowedRead — read and zero%/near% as N-day window daily sums (floored integer %).
// The parser is the shared windowedDaily reader (a single source with report). Lookup failures
// are all 0 (a metering indication — it must not break status, the same principle as FallbackCallsOrNull).
func StatusWindowedRead(ctx context.Context, st wire.Store, days int) (read, zeroPct, nearPct int) {
	dayRows, err := WindowedDailyFn(ctx, st, days)
	if err != nil {
		return 0, 0, 0
	}
	var zero, near int
	for _, d := range dayRows {
		read += d.Read
		zero += d.ReadZero
		near += d.ReadNear
	}
	return read, ReadZeroPctFn(read, zero), ReadZeroPctFn(read, near)
}

// StatusWindowedBytes — G5: the N-day window sum of hot (injection) and read output bytes.
// Calls the real WindowedDaily implementation directly (a single parser source — the seam's DailyRow
// carries only read metering, so the bytes are summed here directly). Lookup failures and corrupt
// values are all 0 (a metering indication — it must not break status, the same principle
// as StatusWindowedRead).
func StatusWindowedBytes(ctx context.Context, st wire.Store, days int) (hotBytes, readBytes int) {
	rows, err := WindowedDaily(ctx, st, days)
	if err != nil {
		return 0, 0
	}
	for _, d := range rows {
		hotBytes += d.HotBytes
		readBytes += d.ReadBytes
	}
	return hotBytes, readBytes
}

// statusAsOfOutput — the output when --as-of is given. Embeds the existing StatusOutput
// fields, keeping the key order and appending as_of{hash,commit_date} at the end.
// From v0.9.6 it embeds statusV096Output (ending in read_fallback_calls); from v0.18.0,
// statusV0180Output (ending in the windowed read metrics).
// The as_of shape is identical to read --as-of (asOfInfo) — consumers parse one form
// (spec §1 "as_of hash/time", consistent with the e2e asOfHash). StatusOutput itself is unchanged
// (unspecified means the output is unchanged — inheriting the last_* principle).
type statusAsOfOutput struct {
	statusV0180Output
	AsOf knowledge.AsOfInfo `json:"as_of"`
}

// FallbackCallsOrNull — looks up the meta read_fallback_calls (fallback metering).
// Absent, corrupt (non-integer), or lookup errors are nil (JSON null) — being a metering
// indication, it must not break status (the same principle as lastSeenOrNull).
func FallbackCallsOrNull(getKV func(string, string) (string, error)) *int {
	v, err := getKV("read_fallback_calls", "")
	if err != nil || v == "" {
		return nil
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return nil
	}
	return &n
}

// lastSeenOrNull — looks up the RFC3339 last_* keys in meta. Missing or corrupt keys (non-RFC3339)
// are nil (JSON null). Being a metering indication, lookup errors also silently fall back to null.
func lastSeenOrNull(getKV func(string, string) (string, error), key string) *string {
	v, err := getKV(key, "")
	if err != nil || v == "" {
		return nil
	}
	if _, perr := time.Parse(time.RFC3339, strings.TrimSpace(v)); perr != nil {
		return nil
	}
	s := strings.TrimSpace(v)
	return &s
}

// lastReadAtOrNull — the read path no longer writes meta last_read_at
// (writes 4->2 — RecordReadUsage abolished). Reports the more recent of the residual meta
// value and read_events' latest time (max(ts)) — the same merge rule as doctor's
// checkLastSeen. Lookup failures and unmappable cells keep the existing meta value (silent).
func lastReadAtOrNull(ctx context.Context, st wire.Store, getKV func(string, string) (string, error)) *string {
	out := lastSeenOrNull(getKV, "last_read_at")
	rows, err := wire.QueryOnce(ctx, st, `SELECT max(ts) FROM read_events`, nil)
	if err == nil && len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			s := t.UTC().Format(time.RFC3339)
			if out == nil {
				out = &s
			} else if prev, perr := time.Parse(time.RFC3339, *out); perr == nil && t.After(prev) {
				out = &s
			}
		}
	}
	return out
}

// RunStatus — reports server liveness, schema compatibility, and the pending backlog count.
// With --as-of <spec>, it splices AS OF '<hash>' into the count queries, reports the past
// snapshot's pending count, and appends as_of (the normalized hash) at the end of the output.
// An invalid spec is ErrInvalidAsOf -> exit 3.
func RunStatus(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	asOfSpec := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--as-of" {
			if i+1 >= len(args) {
				// a --as-of with no value is also an invalid spec (empty spec) exiting 3.
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf)))
				return 3
			}
			asOfSpec, i = args[i+1], i+1
		}
	}
	asOfHash, asOfDate := "", ""
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			if errors.Is(err, ErrInvalidAsOf) {
				return 3
			}
			return store.MapExit(err)
		}
		asOfHash = hash
		// attach the commit time (the same query as read --as-of, silent fallback — on a metadata
		// lookup failure, the empty value, §13). LogEntries{Hash} — dialect SQL belongs to lib/db.
		if entries, err := st.LogEntries(ctx, wire.LogQuery{Hash: hash}); err == nil && len(entries) > 0 {
			asOfDate = knowledge.FmtAsOfDate(entries[0].Date)
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	version := ""
	if len(rows) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	// the meta.schema_version compatibility check (PLAN §10): only versions this CLI knows are allowed.
	// The allowed range derives from db.LatestSchemaVersion (1..latest — the promotion order
	// of each version is guaranteed by the db.SchemaGates continuity). A hardcoded list goes
	// stale at every schema promotion (a v9 DB is a normal state yet exits 3 — audit F1).
	ver, perr := strconv.Atoi(strings.TrimSpace(version))
	if perr != nil || ver < 1 || ver > db.LatestSchemaVersion {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "schema_version",
			Expect: "1~" + strconv.Itoa(db.LatestSchemaVersion)}))
		return 3
	}
	// the windowed read metrics (window = daily_retention_days) —
	// reported alongside the cumulative values. The *_14d field names are kept for backward compatibility (the actual window is read_window_days).
	winDays := knowledge.WriteThresholds(ctx, st).DailyRetentionD
	winRead, winZeroPct, winNearPct := StatusWindowedRead(ctx, st, winDays)
	// G5 — the windowed sum of hot and read output bytes (lookup failure falls back to 0).
	winHotBytes, winReadBytes := StatusWindowedBytes(ctx, st, winDays)
	// per-surface bytes p95 (spec §2.3). read uses the read_events bytes
	// distribution; hot derives from hot_events x summary lengths (DerivedHotStats). In as-of mode
	// they are count-snapshot-only (metering queries follow the current-state principle), hence 0.
	// A lookup failure is 0 (a metering indication — it must not break status, the same principle
	// as StatusWindowedBytes).
	readBytesP95, hotBytesP95 := 0, 0
	if asOfSpec == "" {
		readBytesP95 = ReadBytesP95Of(ctx, st)
		_, hotBytesP95 = DerivedHotStats(ctx, st)
	}
	// with --as-of, only the count queries get the AS OF splice. Meta and metering lookups
	// use the current state as-is. The current-time count uses the single
	// countPending implementation.
	pending := 0
	if asOfHash != "" {
		// the same rule as read --as-of: revalidate the hash charset before the query —
		// never trust an engine return value by putting it straight into a query condition. A violation
		// ends in an error (blocking injection via a tainted hash). QueryAsOf enforces the
		// whitelist table and hash revalidation doubly in lib/db.
		if !knowledge.ValidAsOfHash(asOfHash) {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("as-of hash has invalid characters: %q", asOfHash)))
			return 1
		}
		asOfRows, err := st.QueryAsOf(ctx, wire.AsOfQuery{Hash: asOfHash, Table: "knowledge",
			Columns: []string{"count(*)"}, Where: "status = 'pending' AND invalid_at IS NULL"})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		if len(asOfRows) > 0 {
			fmt.Sscan(fmt.Sprint(asOfRows[0][0]), &pending)
		}
	} else {
		n, cerr := knowledge.CountPending(ctx, st)
		if cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
		pending = n
	}
	// status is a separate process, so the in-memory samples are
	// empty — it loads the meta hot_stats baseline and reports the previous run's cumulative.
	LoadHotStatsFromMetaFn(ctx, st)
	calls, p95, _ := HotStatsFn()
	readCalls, readZero := LoadReadStatsFromMetaFn(ctx, st)
	// the 3-path intake last-seen (meta last_*_at) — absent or corrupt is null.
	getKV := knowledge.MetaGetKV(ctx, st)
	base := statusV096Output{StatusOutput: StatusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95, ReadCalls: readCalls, ReadZeroPct: ReadZeroPctFn(readCalls, readZero),
		LastWriteAt: lastSeenOrNull(getKV, "last_write_at"),
		LastHotAt:   lastSeenOrNull(getKV, "last_hot_at"),
		// last_read_at merges the residual meta value with the read_events-derived one.
		LastReadAt: lastReadAtOrNull(ctx, st, getKV)},
		ReadFallbackCalls: FallbackCallsOrNull(getKV)}
	var b []byte
	if asOfHash != "" {
		b, _ = json.Marshal(statusAsOfOutput{statusV0180Output: statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes,
			ReadBytesP95: readBytesP95, HotBytesP95: hotBytesP95},
			AsOf: knowledge.AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
	} else {
		b, _ = json.Marshal(statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes,
			ReadBytesP95: readBytesP95, HotBytesP95: hotBytesP95})
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
