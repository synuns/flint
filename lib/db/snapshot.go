// snapshot.go — the threshold-change accounting circuit (schema v13 A2,
// v1.19.0). Once a day (a 24h meta gate + a 1h in-process cache — the
// maybePruneAfterRead pattern), the current value of EVERY thresholds
// registry key is compared with the key's last snapshot row; changed
// keys only are appended to threshold_snapshots (append-only — the
// fired-motivation column is deferred to the v1.23.0 registry). The
// first expiry on an empty table seeds the full key set as the baseline
// (old_value empty — no prior). Retention follows
// threshold_snapshots_retention_days (the thresholds registry). All
// failures pass quietly (the metering principle) and the DB gate is
// stamped only on success so a failed pass keeps the retry open.
package db

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// thresholdSnapshotGateKey — the 24h-gate meta key (the last_*_at
// RFC3339 convention; a key separate from the intake-metering times).
const thresholdSnapshotGateKey = "last_threshold_snapshot_at"

// snapshotGateCheckedAt — the in-process gate-recheck cache (the
// pruneGateCheckedAt convention): the unix seconds of the last gate
// check, 0 = unchecked. Shared regardless of Repo instance; shrinks the
// read hot path to one gate SELECT per hour.
var snapshotGateCheckedAt atomic.Int64

// snapshotGateCacheTTL — the in-process gate-cache lifetime (1h, shorter
// than the 24h DB gate — single-shot CLIs and long-lived processes
// re-evaluate the gate at least hourly).
const snapshotGateCacheTTL = time.Hour

// snapshotGateDue — the gate-decision pure function (the pruneGateDue
// twin): false if last parses as RFC3339 and is within 24h; otherwise
// (absent·blank·corrupt·24h elapsed) true.
func snapshotGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return now.Sub(t) >= 24*time.Hour
}

// snapshotGateProbe — the gate-cache verdict (the pruneGateProbe twin):
// false within the TTL of the last check (the gate meta SELECT is
// skipped), true on elapsed or first-time (stamping the check time).
func snapshotGateProbe(now time.Time) bool {
	if last := snapshotGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < snapshotGateCacheTTL {
		return false
	}
	snapshotGateCheckedAt.Store(now.Unix())
	return true
}

// thresholdValueText — the registry default's text notation (the same
// rules as the TUNING-table Default derivation: int as-is, float64 with
// the minimal-digit format).
func thresholdValueText(def any) string {
	switch d := def.(type) {
	case int:
		return strconv.Itoa(d)
	case float64:
		return strconv.FormatFloat(d, 'f', -1, 64)
	}
	return ""
}

// currentThresholdValues — the current value text of every registry key
// (one meta SELECT via the registry-derived IN path): the meta override
// when present, else the registry default notation. Every key is
// present in the result — the full-key load that seeds the baseline and
// feeds the diff.
func (r *Repo) currentThresholdValues(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key = ANY($1)`, wire.ThresholdsKeys())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	overrides := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		overrides[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(overrides))
	for _, m := range wire.ThresholdMetas() {
		if v, ok := overrides[m.Key]; ok {
			out[m.Key] = v
		} else {
			out[m.Key] = thresholdValueText(m.Default)
		}
	}
	return out, nil
}

// lastThresholdSnapshots — each key's latest new_value (one SELECT over
// the append-only history, ts-ascending — the last row per key wins;
// the (key, ts DESC) index keeps the per-key scan path). An empty table
// yields an empty map (the first-expiry seeding premise).
func (r *Repo) lastThresholdSnapshots(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, new_value FROM threshold_snapshots ORDER BY ts ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// snapshotThresholds — one gated pass: load the full-key current values
// (1 SELECT), load the last snapshot per key (1 SELECT), append the
// changed rows only (INSERT per changed row — 0 on a no-change pass,
// the full key set on the empty-table first expiry), then apply the
// retention delete. Round trips: gate 1 + load 1 + last 1 + INSERT N.
func (r *Repo) snapshotThresholds(ctx context.Context) error {
	current, err := r.currentThresholdValues(ctx)
	if err != nil {
		return err
	}
	last, err := r.lastThresholdSnapshots(ctx)
	if err != nil {
		return err
	}
	for _, m := range wire.ThresholdMetas() {
		prev, seen := last[m.Key]
		if seen && prev == current[m.Key] {
			continue // unchanged — no row (the no-change 0-row contract)
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO threshold_snapshots (key, old_value, new_value)
			 VALUES ($1, $2, $3)`, m.Key, prev, current[m.Key]); err != nil {
			return err
		}
	}
	return r.pruneThresholdSnapshots(ctx)
}

// pruneThresholdSnapshots — deletes snapshot rows past
// threshold_snapshots_retention_days (the thresholds registry; absent·
// corrupt·0 fall back to the DefaultThresholds 90 — the
// thresholdsForPrune convention).
func (r *Repo) pruneThresholdSnapshots(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "threshold_snapshots_retention_days")
	days := th.ThresholdSnapshotsRetentionD
	if days <= 0 {
		days = store.DefaultThresholds().ThresholdSnapshotsRetentionD
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM threshold_snapshots WHERE ts < $1`,
		time.Now().UTC().Add(-time.Duration(days)*24*time.Hour))
	return err
}

// maybeSnapshotThresholds — the read-path hook (the RecordReadEvent
// tail, one line). Gate-checked per hour in-process, gated per day in
// the DB (stamped only on success — a failed pass keeps the retry
// open). Parallel passes are harmless: the append is idempotent by the
// value comparison, and a duplicate baseline row is self-correcting on
// the next pass. Failures pass quietly (the metering principle).
func (r *Repo) maybeSnapshotThresholds(ctx context.Context) {
	if !snapshotGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, thresholdSnapshotGateKey).Scan(&last)
	if !snapshotGateDue(last, time.Now().UTC()) {
		return
	}
	if err := r.snapshotThresholds(ctx); err != nil {
		return
	}
	_, _ = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		thresholdSnapshotGateKey, time.Now().UTC().Format(time.RFC3339))
}
