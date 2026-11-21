// rollup.go — the weekly long-baseline rollup circuit (schema v13 A3,
// v1.19.0). Once a day (a 24h meta gate + a 1h in-process cache — the
// maybePruneAfterRead/snapshot.go pattern), the FULLY-ELAPSED last week
// (UTC Monday boundary) is aggregated from the raw read_events into
// weekly_rollups as num/den counter pairs — no derived ratios stored
// (the ratio is computed at consumption). The num/den definitions
// follow ops/report.go's EffectOutput (METRICS.md is the single
// documentation source): den = in-window read events for all three
// metrics; zero_pct num = zero rows; fallback_pct num = fallback rows;
// pair_pct num = reads followed within pair_window_minutes by a
// knowledge write (report's time-window pairing — the write is
// ingested_at in (0, window] after the read; report's session-interval
// refinement stays in report, the precise window source — the rollup
// is the long baseline). Missing weeks are not backfilled (the target
// is the last week only). Re-aggregation UPSERTs the same values
// (idempotent — a late-arriving raw event converges on the next daily
// pass). Retention follows weekly_rollups_retention_days. All failures
// pass quietly (the metering principle) and the DB gate is stamped only
// on success. The hook runs BEFORE the prune hook (aggregate then
// clean — the RecordReadEvent tail order is pinned by a guard test).
package db

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/synuns/flint/lib/store"
)

// rollupGateKey — the 24h-gate meta key (the last_*_at RFC3339
// convention).
const rollupGateKey = "last_rollup_at"

// rollupGateCheckedAt — the in-process gate-recheck cache (the
// pruneGateCheckedAt/snapshotGateCheckedAt convention).
var rollupGateCheckedAt atomic.Int64

// rollupGateCacheTTL — the in-process gate-cache lifetime (1h, shorter
// than the 24h DB gate).
const rollupGateCacheTTL = time.Hour

// rollupGateDue — the gate-decision pure function (the pruneGateDue
// twin): false if last parses as RFC3339 and is within 24h; otherwise
// true (a future-dated stamp is immediately due — the gateStampDue
// guard, v1.19.1).
func rollupGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return gateStampDue(t, now)
}

// rollupGateProbe — the gate-cache verdict (the pruneGateProbe twin).
func rollupGateProbe(now time.Time) bool {
	if last := rollupGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < rollupGateCacheTTL {
		return false
	}
	rollupGateCheckedAt.Store(now.Unix())
	return true
}

// lastWeekStart — the UTC Monday midnight starting the fully-elapsed
// week before the current one (the rollover target). The current week —
// even its Monday 00:00 boundary — is never a target (ISO Monday-based
// weeks; a Monday input maps back exactly 7 days).
func lastWeekStart(now time.Time) time.Time {
	now = now.UTC()
	y, m, d := now.Date()
	monday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	sinceMonday := (int(now.Weekday()) + 6) % 7
	return monday.Add(-time.Duration(sinceMonday+7) * 24 * time.Hour)
}

// rollupLastWeek — one gated pass: aggregate last week's num/den (1
// query) and UPSERT the 3 metric rows (INSERT N=3). An empty week (den
// = 0) writes no row — a 0/0 ratio is meaningless, and the absent row
// is itself the "no reads this week" record. Then the retention delete
// runs. Round trips: gate 1 + aggregate 1 + UPSERT N.
func (r *Repo) rollupLastWeek(ctx context.Context) error {
	ws := lastWeekStart(time.Now().UTC())
	we := ws.Add(7 * 24 * time.Hour)
	th, _ := r.thresholdsForPrune(ctx, "pair_window_minutes")
	window := th.PairWindowMin
	if window <= 0 {
		window = store.DefaultThresholds().PairWindowMin
	}
	windowSecs := float64(window) * 60
	var den, zeroN, fallbackN, pairN int64
	// The pairing subquery is report's time-window definition: a
	// knowledge write (ingested_at) inside (0, window] after the read.
	// count(CASE …) over FILTER — Doltgres-portable conditional counts;
	// the window predicate is epoch-second arithmetic (Doltgres lacks
	// integer * interval — measured).
	err := r.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(CASE WHEN zero THEN 1 END),
		       count(CASE WHEN fallback THEN 1 END),
		       count(CASE WHEN EXISTS (
		           SELECT 1 FROM knowledge k
		           WHERE k.ingested_at > read_events.ts
		             AND extract(epoch from (k.ingested_at - read_events.ts)) <= $3
		       ) THEN 1 END)
		FROM read_events
		WHERE ts >= $1 AND ts < $2`, ws, we, windowSecs).
		Scan(&den, &zeroN, &fallbackN, &pairN)
	if err != nil {
		return err
	}
	if den > 0 {
		for _, m := range []struct {
			name string
			num  int64
		}{
			{"zero_pct", zeroN},
			{"fallback_pct", fallbackN},
			{"pair_pct", pairN},
		} {
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO weekly_rollups (week_start, metric, num, den)
				 VALUES ($1, $2, $3, $4)
				 ON CONFLICT (week_start, metric)
				 DO UPDATE SET num = EXCLUDED.num, den = EXCLUDED.den`,
				ws, m.name, m.num, den); err != nil {
				return err
			}
		}
	}
	return r.pruneWeeklyRollups(ctx)
}

// pruneWeeklyRollups — deletes rollup rows whose week_start is past
// weekly_rollups_retention_days (the thresholds registry; absent·
// corrupt·0 fall back to the DefaultThresholds 90 — the
// thresholdsForPrune convention). Weeks older than the horizon have
// served the long-baseline window and are dropped whole-week (the
// Monday boundary minus the retention span).
func (r *Repo) pruneWeeklyRollups(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "weekly_rollups_retention_days")
	cutoff := retentionCutoffDays(th.WeeklyRollupsRetentionD,
		store.DefaultThresholds().WeeklyRollupsRetentionD, time.Now().UTC())
	_, err := r.pool.Exec(ctx,
		`DELETE FROM weekly_rollups WHERE week_start < $1`, cutoff)
	return err
}

// maybeRollupWeekly — the read-path hook (the RecordReadEvent tail,
// FIRST — before the prune hook so a week's raw events are never
// cleaned before the rollup confirms). Gate-checked per hour
// in-process, gated per day in the DB (stamped only on success — a
// failed pass keeps the retry open). Parallel passes are harmless: the
// UPSERT is idempotent by the (week_start, metric) key. Failures pass
// quietly (the metering principle).
func (r *Repo) maybeRollupWeekly(ctx context.Context) {
	if !rollupGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, rollupGateKey).Scan(&last)
	if !rollupGateDue(last, time.Now().UTC()) {
		return
	}
	if err := r.rollupLastWeek(ctx); err != nil {
		return
	}
	_, _ = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		rollupGateKey, time.Now().UTC().Format(time.RFC3339))
}
