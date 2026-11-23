package db

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/synuns/flint/lib/store"
)

const rollupGateKey = "last_rollup_at"

var rollupGateCheckedAt atomic.Int64

const rollupGateCacheTTL = time.Hour

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

func rollupGateProbe(now time.Time) bool {
	if last := rollupGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < rollupGateCacheTTL {
		return false
	}
	rollupGateCheckedAt.Store(now.Unix())
	return true
}

func lastWeekStart(now time.Time) time.Time {
	now = now.UTC()
	y, m, d := now.Date()
	monday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	sinceMonday := (int(now.Weekday()) + 6) % 7
	return monday.Add(-time.Duration(sinceMonday+7) * 24 * time.Hour)
}

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

func (r *Repo) pruneWeeklyRollups(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "weekly_rollups_retention_days")
	cutoff := retentionCutoffDays(th.WeeklyRollupsRetentionD,
		store.DefaultThresholds().WeeklyRollupsRetentionD, time.Now().UTC())
	_, err := r.pool.Exec(ctx,
		`DELETE FROM weekly_rollups WHERE week_start < $1`, cutoff)
	return err
}

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
