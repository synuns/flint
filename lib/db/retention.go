// retention.go — the shared retention-cutoff·gate-stamp guards of the
// daily maintenance circuits (schema v13, v1.19.1). Two overflow-class
// defects surfaced by the R71-R74 audit live here once so every circuit
// (snapshot·rollup·event prune) shares the same hardening: a retention
// days override past the Duration ceiling wraps the naive
// time.Duration(days)*24h arithmetic into a FUTURE cutoff (a
// full-table delete), and a future-dated gate stamp closes the 24h
// gate for its whole span (now.Sub(t) runs negative). Both are
// override-space defenses — no defaults or tuning guidance change.
package db

import (
	"time"

	"github.com/synuns/flint/lib/store"
)

// maxRetentionDays — the retention-days clamp ceiling: 106751 days is
// the largest day count whose *24h Duration stays inside int64
// (106752+ wraps negative → a future cutoff → the whole table pruned).
// Anything past it is treated as "retain everything reachable" — the
// cutoff clamps to the ceiling rather than the override's literal span.
const maxRetentionDays = 106751

// retentionCutoffDays — the retention cutoff for days against now:
// days <= 0 falls back to def (the thresholdsForPrune convention kept
// at the single choke point), then days clamps to [0, maxRetentionDays]
// and the cutoff is derived via AddDate (calendar arithmetic — no
// Duration multiplication, so no int64 wrap at any day count).
func retentionCutoffDays(days, def int, now time.Time) time.Time {
	if days <= 0 {
		days = def
	}
	if days > maxRetentionDays {
		days = maxRetentionDays
	}
	if days < 0 {
		days = 0
	}
	return now.AddDate(0, 0, -days)
}

// defForRetention — the DefaultThresholds retention span of one event
// prune table (the days<=0 fallback argument of retentionCutoffDays).
// The pairing mirrors PruneReadEvents' table→threshold-field map.
func defForRetention(def store.Thresholds, table string) int {
	switch table {
	case "hot_events":
		return def.HotRetentionD
	case "knowledge_reads":
		return def.KreadsRetentionD
	default: // read_events·session_starts
		return def.EventsRetentionD
	}
}

// gateStampDue — the 24h-gate expiry verdict for a parsed gate stamp:
// a stamp dated in the FUTURE is immediately due (its whole forward
// span would otherwise hold the gate closed — now.Sub(t) negative);
// otherwise due exactly at 24h elapsed. The three gate circuits
// (snapshot·rollup·event prune) share this via their pure functions.
func gateStampDue(t, now time.Time) bool {
	if t.After(now) {
		return true
	}
	return now.Sub(t) >= 24*time.Hour
}
