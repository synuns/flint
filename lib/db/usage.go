// usage.go — playbook use logs·success rate. Under the no-new-tables
// constraint (schema v2 kept), records via increments of the meta keys
// pb_<id>_uses/pb_<id>_ok. Use logging is intentionally non-idempotent
// — the same call counted twice is a log, not a defect.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/lib/store"
)

// Stat — per-playbook usage statistics (an internal utility — no
// consuming CLI path).
type Stat struct {
	ID   int
	Uses int
	OK   int
	Rate float64 // OK/Uses, rounded to the third digit (2/3 → 0.667)
}

// LogPlaybookUse records one playbook use as meta increments. With
// ok=true, both uses·ok increment; otherwise uses only (failures still
// count as uses).
func (r *Repo) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_uses", id)); err != nil {
		return fmt.Errorf("log playbook use: uses: %w", err)
	}
	if ok {
		if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_ok", id)); err != nil {
			return fmt.Errorf("log playbook use: ok: %w", err)
		}
	}
	return nil
}

// bumpMeta increments a meta key by 1 with an upsert (a single
// statement — no read-write race).
func bumpMeta(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = (meta.value::bigint + 1)::text`, key)
	return err
}

// usageRetryBackoff — the 40001 (serialization failure) retry spacing
// for RecordUsage writes. Under parallel startup (shared-DB pool suite
// load), the daily-counter upsert optimistically conflicts with another
// session's commit — the same recovery pattern as drift DROP·promotion
// commits.
var usageRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// RecordUsage — records one intake-metering round in meta. The bulk
// count-increment body is RecordUsageCount — this method delegates with
// count=1.
func (r *Repo) RecordUsage(ctx context.Context, kind, source string) error {
	return r.RecordUsageCount(ctx, kind, source, 1)
}

// RecordUsageCount — bulk-increments count rounds of intake metering
// into daily counters (avoiding the O(N) read-writes of dup N
// accounting). kind = write|hot|read|dup + dup_hook|dup_manual|dup_mcp|dup_correction|dup_gap
// (per-source dup, additive — v1.12.0 gap added) + brief (brief intake metering, additive).
// Upserts last_<kind>_at as RFC3339 (dup carries no time key — the
// write time is reused) and increments the `daily:<kind>:YYYY-MM-DD`
// integer flat key (the legacy `daily:YYYY-MM-DD` JSON counter
// abolished — reads parse directly via report's WindowedDaily). write
// also increments the source counters (hook|manual|mcp|correction|gap — the
// sourceRegistry of knowledge/write.go, v1.12.0 gap added) in parallel.
// daily keys beyond 14 days are cleaned by PruneDaily, run only when
// today's daily key was newly created (the date-rollover first event) —
// a DELETE on every record wastes the hot path and duplicate writes run
// it twice. A cleanup failure never affects the record (ignored). The
// flat increment is a single UPSERT statement in the bumpMeta pattern
// (spec §1.1 — an increment impossible with JSON values because
// doltgres lacks jsonb_object_agg, working around a limitation confirmed
// 2026-10-22). The read-modify-write round trip and the 40001 retry
// window disappear. However, Doltgres accepts concurrent UPSERTs of the
// same row as last-write-wins without error (demonstrated 2026-11-13) —
// total-count consistency under parallel concurrent increments is not
// guaranteed, same as the legacy JSON contract (metering principle:
// loss < call failure). Corrupted existing flat values are rewritten
// from 0. The retry path (recordWithRetry) is kept to preserve the
// PruneDaily rollover hook, and exhaustion passes quietly — RecordUsage
// follows the metering principle already applied by the callers
// (write·read·hot) discarding errors.
func (r *Repo) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	switch kind {
	case "write", "hot", "read", "dup", "dup_hook", "dup_manual", "dup_mcp", "dup_correction", "dup_gap", "brief":
	default:
		return fmt.Errorf("record usage: invalid kind %q", kind)
	}
	if count < 1 {
		return fmt.Errorf("record usage: invalid count %d", count)
	}
	delta := map[string]int{kind: count}
	if kind == "write" && source != "" {
		delta[source] = count
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {
		// The dup family (total dup·dup_<source>) records no last_ time
		// key (contract inherited — the write time is reused; ruling:
		// prevent unused-meta-key accumulation).
		if !strings.HasPrefix(kind, "dup") {
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
				"last_"+kind+"_at", now); err != nil {
				return false, fmt.Errorf("record usage: last_%s_at: %w", kind, err)
			}
		}
		created := false
		for k, n := range delta {
			made, err := r.bumpDailyFlat(ctx, day, k, n)
			if err != nil {
				return false, fmt.Errorf("record usage: daily: %w", err)
			}
			created = created || made
		}
		return created, nil
	})
	return nil
}

// RecordBytes — byte metering: increments one surface round's output
// bytes into the daily:<kind>_bytes:<date> integer flat key (spec §1 —
// read|hot|brief). Corrupted existing flat values are rewritten from 0
// (inheriting the bumpDailyFlat contract). The same layer·retention
// window as intake metering (RecordUsageCount) but with no last_ time
// key (reuses the count metering's last_<kind>_at — ruling: prevent
// unused-meta-key accumulation). n=0 is valid (a zero-hit read's 0
// bytes — call-count denominator consistency); negative·invalid kinds
// are rejected. A corrupted existing daily JSON is rewritten from 0
// (inheriting the bumpDaily contract). Bulk-write failure·retry
// exhaustion pass quietly (the §13 metering principle — the same
// recordWithRetry path as RecordUsageCount).
func (r *Repo) RecordBytes(ctx context.Context, kind string, n int) error {
	switch kind {
	case "read", "hot", "brief":
	default:
		return fmt.Errorf("record bytes: invalid kind %q", kind)
	}
	if n < 0 {
		return fmt.Errorf("record bytes: invalid n %d", n)
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {
		made, err := r.bumpDailyFlat(ctx, day, kind+"_bytes", n)
		if err != nil {
			return false, fmt.Errorf("record bytes: daily: %w", err)
		}
		return made, nil
	})
	return nil
}

// Querier — the minimal meta-access interface. wire.Store (and its
// fakes) structurally satisfy it — command-layer counters reuse this
// package's single-UPSERT implementation without db.Repo (keeping the
// db-ownership principle for write SQL literals).
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
}

// BumpDailyFlatKey — the query body incrementing the
// `daily:<kind>:<date>` flat-key counter (review counters — the
// approve·maintain·import outcomes). Runs the same statement·corruption
// guard as bumpDailyFlat (not ^[0-9]+$ → rewrite n) against an
// arbitrary Querier (via the command side's wire.Store). A
// non-idempotent increment, so it fires once without retry (the
// BumpMetaKV contract) — errors return as-is and the caller absorbs
// them under the metering principle. The kind whitelist belongs to the
// caller (knowledge.BumpCurationCounter) — here only key
// assembly·increment.
func BumpDailyFlatKey(ctx context.Context, q Querier, kind, day string, n int) error {
	key := "daily:" + kind + ":" + day
	// The increment value arrives via EXCLUDED.value — reusing the same
	// placeholder ($2) fails Doltgres extended-protocol index inference
	// (inherited from the bumpDailyFlat comment, confirmed 2026-11-13).
	_, err := q.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value =
		   CASE WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + EXCLUDED.value::bigint)::text
		        ELSE EXCLUDED.value END`, key, strconv.Itoa(n))
	if err != nil {
		return fmt.Errorf("bump daily flat key: %w", err)
	}
	return nil
}

// ReadEvent — one effectiveness-metering event row (struct-ified —
// converging the scattered 7-parameter call sites into one value,
// metrics·storage unchanged). IDs is the returned knowledge id CSV
// (top 10); FirstRank is the first topic-match rank (0 = no match·
// fallback excluded).
// Three additional columns (additive — unfilled rows default to 0·
// empty): Bytes (output bytes — the same definition as G5, len of the
// final output)·ZeroReason ('unowned'|'owned_miss'|empty, only on
// zero=1 rows — the spec §2.2 zero-hit decomposition)·Source (the
// 'hook'|'cli'|'mcp' channel label — hook/cli by session-label
// presence; the MCP path is passed by the caller).
type ReadEvent struct {
	Hits       int
	Zero       bool
	Near       bool
	Fallback   bool
	Session    string
	IDs        string
	FirstRank  int
	Bytes      int
	ZeroReason string
	Source     string
}

// RecordReadEvent — records one effectiveness-metering event row
// (docs/METRICS.md §4). Leaves only one read's numbers·flags·session
// label (no query body·result content — the no-personal-wording
// principle). Adds the returned knowledge id CSV (ids, top 10) — and
// the first topic-match rank (firstRank, 0 = no match·fallback
// excluded). Converged into the single ReadEvent struct value. The
// source of usage-chain computation. Also records the 3 columns
// bytes·zero_reason·source (schema v10). INSERT-only, so there is no
// read-write race (40001) and it fires once without retry. The success
// path also fires maybePruneAfterRead (the 24h-gated cleanup hook) —
// cleanup must happen even in the structure where read bypasses
// RecordUsage (see the hook comment below).
func (r *Repo) RecordReadEvent(ctx context.Context, ev ReadEvent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank, bytes, zero_reason, source)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs, ev.FirstRank,
		ev.Bytes, ev.ZeroReason, ev.Source)
	if err != nil {
		return fmt.Errorf("record read event: %w", err)
	}
	r.maybePruneAfterRead(ctx)
	return nil
}

// pruneReadGateKey — the 24h-gate meta key of the read-path cleanup
// hook (following the last_*_at RFC3339 convention). A key separate
// from last_read_at (not the intake-metering time).
const pruneReadGateKey = "last_events_prune_at"

// pruneGateDue — the gate-decision pure function (unit-test target):
// false if last parses as RFC3339 and is within 24h (cleanup skipped);
// otherwise (absent·blank·corrupt·24h elapsed) true.
func pruneGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return now.Sub(t) >= 24*time.Hour
}

// pruneGateCheckedAt — the in-process gate-recheck cache (performance):
// the unix seconds of the last gate check (atomic — shared regardless
// of Repo instance). 0 = unchecked. Shrinks the read hot path — which
// used to leave a meta gate SELECT on every RecordReadEvent — to one
// round trip per hour.
var pruneGateCheckedAt atomic.Int64

// pruneGateCacheTTL — the in-process gate-cache lifetime. 1h, shorter
// than the DB gate (24h) — both single-shot CLIs and long-lived
// processes re-evaluate the gate at least hourly so cleanup never slips
// a whole day.
const pruneGateCacheTTL = time.Hour

// pruneGateProbe — the gate-cache verdict (pure·unit-test target):
// false if within the TTL of the last check (gate meta SELECT skipped —
// 0 rechecks within the TTL); true if elapsed or first-time, stamping
// the check time (1 SELECT). The stamp reflects the gate-check moment —
// even a later cleanup failure leaves the DB gate unstamped, keeping
// retry open for other processes·after TTL expiry.
func pruneGateProbe(now time.Time) bool {
	if last := pruneGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < pruneGateCacheTTL {
		return false
	}
	pruneGateCheckedAt.Store(now.Unix())
	return true
}

// maybePruneAfterRead — the cleanup hook for read-only sessions. With
// RecordReadUsage (the read daily write) abolished, the read path no
// longer passes through recordWithRetry (the rollover hook) — in
// sessions without a write, PruneReadEvents never fires and read_events
// grows unbounded past the retention horizon. On the RecordReadEvent
// success path, when the last_events_prune_at gate opens (24h since
// the last cleanup — a DELETE·meta round trip on every read wastes the
// hot path), it runs PruneDaily·PruneReadEvents. Gate rechecking is
// shrunk to the in-process 1h cache (pruneGateProbe) — the DB gate
// (24h) SELECT runs only when the cache misses. The DB gate is stamped
// only on cleanup success so a failed cleanup is not buried for a day
// (stamp-then-fail would skip 24h with no retry). Even if parallel
// reads pass simultaneously, the DELETE is idempotent — duplicate
// execution is harmless. Gate-read·cleanup·stamp failures pass quietly
// (the metering principle).
func (r *Repo) maybePruneAfterRead(ctx context.Context) {
	if !pruneGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, pruneReadGateKey).Scan(&last)
	if !pruneGateDue(last, time.Now().UTC()) {
		return
	}
	if err := r.PruneDaily(ctx); err != nil {
		return
	}
	if err := r.PruneReadEvents(ctx); err != nil {
		return
	}
	_, _ = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		pruneReadGateKey, time.Now().UTC().Format(time.RFC3339))
}

// RecordSessionStart — one session-start boundary row (the session-start
// command). The source of the session-interval [start_i, start_{i+1})
// assignment — an internal account with no output, recorded under every
// profile (§13 — not agent exposure).
func (r *Repo) RecordSessionStart(ctx context.Context, session string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO session_starts (session) VALUES ($1)`, session)
	if err != nil {
		return fmt.Errorf("record session start: %w", err)
	}
	return nil
}

// RecordHotEvent — one hot --full injection-exposure row (the knowledge
// id CSV left in the actual output). The source of the hot-exposure
// correction of dead_pct — injection happens only on the --full path
// (inheriting the hot.go contract).
func (r *Repo) RecordHotEvent(ctx context.Context, ids string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO hot_events (ids) VALUES ($1)`, ids)
	if err != nil {
		return fmt.Errorf("record hot event: %w", err)
	}
	return nil
}

// thresholdsForPrune — shared by PruneReadEvents·PruneDaily: reads the
// meta threshold overrides via the registry-derived path
// (store.ParseThresholds — a threshold_meta.go guard target) (removing
// the per-key SELECT+Atoi re-parsing). Lookup failure·absent key·
// corrupt·0 (unset) fall back to defaults (DefaultThresholds) — the
// same outcome as the former per-key convention (only n>0 accepted).
func (r *Repo) thresholdsForPrune(ctx context.Context, keys ...string) (store.Thresholds, error) {
	def := store.DefaultThresholds()
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key = ANY($1)`, keys)
	if err != nil {
		return def, nil // a meta-read failure falls back to defaults (former convention inherited)
	}
	defer rows.Close()
	var kv [][2]any
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		kv = append(kv, [2]any{k, v})
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return def, nil
	}
	if th.EventsRetentionD == 0 {
		th.EventsRetentionD = def.EventsRetentionD
	}
	if th.HotRetentionD == 0 {
		th.HotRetentionD = def.HotRetentionD
	}
	if th.DailyRetentionD == 0 {
		th.DailyRetentionD = def.DailyRetentionD
	}
	if th.KreadsRetentionD == 0 {
		th.KreadsRetentionD = def.KreadsRetentionD
	}
	return th, nil
}

// PruneReadEvents — deletes effectiveness-metering events past the
// retention horizon. read_events·session_starts at 14 days (the same
// policy as the daily counters); hot_events at 30 days (aligned with
// the dead_pct exposure-consistency guard window — at 14 days the guard
// would cover only half the documented contract). Retention days come
// from the meta overrides (events_retention_days·
// hot_events_retention_days, TUNING §2) — absent·corrupt keys yield
// defaults. The per-item recall history knowledge_reads is cleaned by
// the same trigger (initially 14 days·a separate
// knowledge_reads_retention_days override). The per-key direct SELECT
// stopgap for the knowledge_reads retention days is removed — unified
// via thresholdsForPrune (registry-derived store.ParseThresholds). Two
// triggers (firing alongside PruneDaily): ① recordWithRetry's rollover
// hook (today's daily key newly created — the day's first write
// metering), ② maybePruneAfterRead (RecordReadEvent success + the
// last_events_prune_at 24h gate — cleanup happens even in read-only
// sessions). Roughly once a day, so no meta-read round-trip burden.
// Failures pass quietly at the caller (the metering principle).
func (r *Repo) PruneReadEvents(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx,
		"events_retention_days", "hot_events_retention_days",
		"knowledge_reads_retention_days")
	retention := map[string]int{
		"read_events":     th.EventsRetentionD,
		"session_starts":  th.EventsRetentionD,
		"hot_events":      th.HotRetentionD,
		"knowledge_reads": th.KreadsRetentionD,
	}
	now := time.Now().UTC()
	for table, days := range retention {
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE ts < $1`, now.Add(-time.Duration(days)*24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

// recordWithRetry — the daily-increment recording common procedure (for
// RecordUsageCount. RecordReadUsage (the read-kind daily write) is
// abolished — the source of read intake metering is read_events). Runs
// fn with 40001 retries (usageRetryBackoff); when fn reports today's
// daily key newly created (the rollover's first event), runs
// PruneDaily·PruneReadEvents (14-day cleanups each) — a DELETE on every
// record wastes the hot path and duplicate writes run it twice. Retry
// exhaustion·cleanup failures pass quietly (the metering principle — a
// lost use log beats a failed call).
func (r *Repo) recordWithRetry(ctx context.Context, fn func(ctx context.Context, day string) (created bool, err error)) {
	day := time.Now().UTC().Format("2006-01-02")
	created := false
	_ = Retry(ctx, func() error {
		made, err := fn(ctx, day)
		if err != nil {
			return err
		}
		created = created || made
		return nil
	}, usageRetryBackoff)
	if created {
		_ = r.PruneDaily(ctx)
		_ = r.PruneReadEvents(ctx)
	}
}

// bumpRetryBackoff — the 40001 (serialization failure) retry spacing
// for the bumpDailyFlat UPSERT. 3 attempts (initial + 2 retries), waits
// 2ms·8ms — a minimal exponential backoff (4x steps) mindful of test
// speed.
var bumpRetryBackoff = []time.Duration{0, 2 * time.Millisecond, 8 * time.Millisecond}

// retryBump40001 — retries fn (a single UPSERT attempt) at
// bumpRetryBackoff spacing only on SQLSTATE 40001 (serialization
// failure). Non-40001 errors return immediately without retry; after 3
// exhausted attempts, the last 40001 is returned as-is (the caller
// wraps it in the existing shape). A ctx cancellation during a retry
// wait returns ctx.Err().
func retryBump40001(ctx context.Context, fn func() error) error {
	var last error
	for i, d := range bumpRetryBackoff {
		if i > 0 || d > 0 {
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			return err
		}
		last = err
	}
	return last
}

// bumpDailyFlat — increments the `daily:<kind>:<date>` integer-value
// counter. A single atomic UPSERT statement in the bumpMeta pattern
// (spec §1.1) — removes the 40001 retry window of the legacy
// daily:<date> JSON read-modify-write. Parallel loss (total-count
// consistency) is accepted as last-write-wins, same as RecordUsageCount
// (demonstrated: Doltgres accepts concurrent UPSERTs as LWW without
// error — loss remains, which the plan contract permits).
// Unlike the LWW-acceptance demonstration (local, 2026-11-13), the
// deployed Doltgres can return 40001 (CI-demonstrated —
// TestFlatKeyAtomicIncrement racing 8 parallel goroutines). Even a
// single UPSERT is subject to serialization conflicts, so a minimal
// retry is regressed (a conditional regression of the retry removed at
// the flat-key switch — retryBump40001, 3 attempts·2ms·8ms). The
// exhaustion error returns as before (the caller's metering principle —
// quiet ignoring is the caller's responsibility, unchanged).
// A corrupted existing value (non-integer) is rewritten from 0. On new
// key creation (no existing row), returns created=true (the
// PruneDaily rollover trigger of recordWithRetry).
func (r *Repo) bumpDailyFlat(ctx context.Context, day, kind string, n int) (created bool, err error) {
	key := "daily:" + kind + ":" + day
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM meta WHERE key = $1)`, key).Scan(&created); err != nil {
		return false, fmt.Errorf("bump daily flat: exists: %w", err)
	}
	created = !created
	// The increment value arrives via EXCLUDED.value — reusing the same
	// placeholder ($2) fails Doltgres extended-protocol index inference
	// (confirmed 2026-11-13).
	if err := retryBump40001(ctx, func() error {
		_, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value =
			   CASE WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + EXCLUDED.value::bigint)::text
			        ELSE EXCLUDED.value END`, key, strconv.Itoa(n))
		return err
	}); err != nil {
		return false, fmt.Errorf("bump daily flat: upsert: %w", err)
	}
	return created, nil
}

// readDailyFlat — the current value of the daily:<kind>:<date> counter
// (flat key first). When the flat-key row is absent, reads the kind
// from the legacy daily:<date> JSON counter and inherits it (a
// migration-read fallback — preventing loss of existing window
// statistics; read-only: the JSON is not rewritten into flat keys). Corrupt·
// absent·JSON null yield 0 (aligned with bumpDailyFlat's rewrite-from-0
// contract).
func (r *Repo) readDailyFlat(ctx context.Context, kind, day string) (int, error) {
	var val string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, "daily:"+kind+":"+day).Scan(&val)
	switch {
	case err == nil:
		n, perr := strconv.Atoi(val)
		if perr != nil {
			return 0, nil // corrupted flat value — the pre-rewrite observation is 0
		}
		return n, nil
	case errors.Is(err, pgx.ErrNoRows):
		var legacy string
		lerr := r.pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, "daily:"+day).Scan(&legacy)
		if lerr != nil {
			if errors.Is(lerr, pgx.ErrNoRows) {
				return 0, nil
			}
			return 0, fmt.Errorf("read daily flat: legacy: %w", lerr)
		}
		m := map[string]int{}
		if json.Unmarshal([]byte(legacy), &m) != nil || m == nil {
			return 0, nil
		}
		return m[kind], nil
	default:
		return 0, fmt.Errorf("read daily flat: %w", err)
	}
}

// pruneDailyKeyPattern — the retention-target key shape: one or more
// [a-z0-9_]+ segments (kind names·the v1.12.0 gapq:<sha256> hash
// fragment — digits entered the class with the hex digests, and the
// multi-segment `*` covers kind+hash keys) before the trailing UTC date.
// Dateless daily:* keys stay out (the format check before the cast).
const pruneDailyKeyPattern = `^daily:([a-z0-9_]+:)*[0-9]{4}-[0-9]{2}-[0-9]{2}$`

// PruneDaily — deletes daily keys beyond the 14-day retention (today +
// the prior 13 days). Cleans the legacy `daily:<date>` JSON keys, the
// `daily:<kind>:<date>` flat keys, and the v1.12.0 gapq counter keys
// `daily:gapq:<sha256>:<date>` (auto-expiry at the date rollover — the
// same-day aggregation contract). Key dates are UTC (the RecordUsage
// recording basis), so the cutoff day is also taken as UTC (excluding
// server-TZ influence). Keys whose date portion is not a date (daily:*)
// are left alone (a format check before the cast). Retention days come
// from the meta override (daily_retention_days) — re-parsed via the
// derived path (thresholdsForPrune) (absent·corrupt·0 yield defaults).
func (r *Repo) PruneDaily(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "daily_retention_days")
	days := th.DailyRetentionD
	// The cutoff is a Go-computed date parameter (convention — `$1`
	// arithmetic like `date - $1` fails because Doltgres infers $1 as
	// interval; regression defense).
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	_, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key ~ '`+pruneDailyKeyPattern+`'
		   AND substring(key from '[0-9]{4}-[0-9]{2}-[0-9]{2}$')::date <= $1::date`, cutoff)
	return err
}

// PlaybookStats aggregates the pb_* use logs into per-playbook
// statistics (ascending by id). pb_* keys not matching the shape are
// quietly skipped (log parsing, not schema).
func (r *Repo) PlaybookStats(ctx context.Context) ([]Stat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key LIKE 'pb\_%'`)
	if err != nil {
		return nil, fmt.Errorf("playbook stats: query: %w", err)
	}
	defer rows.Close()
	byID := map[int]*Stat{}
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return nil, fmt.Errorf("playbook stats: scan: %w", err)
		}
		idStr := strings.TrimPrefix(key, "pb_")
		isOK := false
		switch {
		case strings.HasSuffix(idStr, "_uses"):
			idStr = strings.TrimSuffix(idStr, "_uses")
		case strings.HasSuffix(idStr, "_ok"):
			idStr = strings.TrimSuffix(idStr, "_ok")
			isOK = true
		default:
			continue
		}
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(val)
		s := byID[id]
		if s == nil {
			s = &Stat{ID: id}
			byID[id] = s
		}
		if isOK {
			s.OK += n
		} else {
			s.Uses += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("playbook stats: rows: %w", err)
	}
	out := make([]Stat, 0, len(byID))
	for _, s := range byID {
		if s.Uses > 0 {
			s.Rate = math.Round(float64(s.OK)/float64(s.Uses)*1000) / 1000
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
