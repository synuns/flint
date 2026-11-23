// thresholds.go — the loadThresholds system and the read metering wiring. A shared
// consumption surface for the knowledge package — residual cli files compile
// unedited via the cliwire_alias.go var aliases. The threshold key list and the
// fallback rules (unset keys use DefaultThresholds). The read metering source is
// read_events (RecordReadEvent) plus the daily:* derived computation — the
// read_stats blob metering is abolished. The residual meta read_stats key on an operational DB is never read (a harmless leftover — delete it via psql if desired).
package wire

import (
	"context"
	"strings"

	"github.com/synuns/flint/lib/store"
)

// ThresholdsKeys — the deterministic list of thresholds meta keys.
// Derived from the threshold_meta.go registry (the key set and order are asserted
// by guard tests). The shared-source contract of the SQL IN, the maintain meta
// audit, and the tuningKeys homogeneity drift tests is unchanged.
func ThresholdsKeys() []string {
	metas := ThresholdMetas()
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Key)
	}
	return out
}

// thresholdsKeySQL — the IN-list SQL fragment (deterministic order).
func thresholdsKeySQL() string {
	return "SELECT key, value FROM meta WHERE key IN ('" +
		strings.Join(ThresholdsKeys(), "','") + "')"
}

// ApplyThresholdsFallback — falls back unset (0) and disallowed negative keys to
// the DefaultThresholds values (the consumer-side completion of the
// ParseThresholds unset-0 convention). Moved from cli maintain.go — residual cli consumers such as tuning.go inherit it via an alias.
func ApplyThresholdsFallback(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	for _, f := range []struct {
		cur   *int
		def   int
		allow bool // when true, negative sentinels like -1 are allowed (RecallWarnWrites)
	}{
		{&th.StaleDays, def.StaleDays, false},
		{&th.StallHotH, def.StallHotH, false},
		{&th.StallWriteH, def.StallWriteH, false},
		{&th.SuggestScanCap, def.SuggestScanCap, false},
		{&th.BackupAgeWarnH, def.BackupAgeWarnH, false},
		{&th.ZeroNearMinRead, def.ZeroNearMinRead, false},
		{&th.ZeroNearWarnPct, def.ZeroNearWarnPct, false},
		{&th.PendingAgeDays, def.PendingAgeDays, false},
		{&th.PendingPromoteDays, def.PendingPromoteDays, false},
		{&th.PendingPromoteMinRunes, def.PendingPromoteMinRunes, false},
		{&th.InvalidatedPruneDays, def.InvalidatedPruneDays, false},
		{&th.RecallWarnWrites, def.RecallWarnWrites, true},
		{&th.DeadDays, def.DeadDays, false},
		{&th.PairWindowMin, def.PairWindowMin, false},
		{&th.ChainWindowH, def.ChainWindowH, false},
		{&th.EventsRetentionD, def.EventsRetentionD, false},
		{&th.HotRetentionD, def.HotRetentionD, false},
		{&th.EventsVolumeWarn, def.EventsVolumeWarn, false},
		{&th.DrillMaxDays, def.DrillMaxDays, false},
		{&th.SecretsScanRows, def.SecretsScanRows, false},
		{&th.SessionKnowledgeDays, def.SessionKnowledgeDays, true}, // the -1 = off sentinel allowed
		{&th.DailyRetentionD, def.DailyRetentionD, false},
		{&th.RecallEventRetentionD, def.RecallEventRetentionD, false},
		{&th.CorrectionDailyMax, def.CorrectionDailyMax, false},
		// the engine-warning thresholds (v1.25.0) — unset 0 would read as
		// "warn at depth 0 / size 0" in doctor, so the fallback is mandatory.
		{&th.DoltLogDepthWarn, def.DoltLogDepthWarn, false},
		{&th.DbSizeWarnMB, def.DbSizeWarnMB, false},
		// the update-check cache age (v1.26.0) — unset 0 would read as
		// "warn at any age" in doctor versions, so the fallback is mandatory.
		{&th.UpdateCacheAgeH, def.UpdateCacheAgeH, false},
	} {
		if *f.cur == 0 || (!f.allow && *f.cur < 0) {
			*f.cur = f.def
		}
	}
	if th.SuggestMinScore <= 0 {
		th.SuggestMinScore = def.SuggestMinScore
	}
	// the correction write default confidence (v1.11.0) — unset (0)/invalid
	// falls back to the default (0 confidence is meaningless as a default).
	if th.CorrectionConfidence <= 0 {
		th.CorrectionConfidence = def.CorrectionConfidence
	}
	if th.ConflictDupThreshold <= 0 {
		th.ConflictDupThreshold = def.ConflictDupThreshold
	}
	return th
}

// LoadThresholdsFn — the LoadThresholds seam (the injection point for
// rrf_k injection-order tests). Cli subpackage tests inherit the existing swap
// convention via the cliwire_alias var (loadThresholdsFn).
var LoadThresholdsFn = LoadThresholds

// LoadThresholds — meta-table key-values -> ParseThresholds (unset keys get
// defaults). The keys (stale_days, stall_hot_h, stall_write_h) and the keys
// (suggest_*, conflict_dup_threshold, backup_age_warn_h, zero_near_*,
// pending_age_days) are left at 0 when unset by ParseThresholds (the
// existing-test-compatibility convention), so they fall back to the DefaultThresholds values.
// A lookup failure propagates the err — the same as the existing convention where the caller (cli doctor, etc.) falls back to DefaultThresholds.
func LoadThresholds(ctx context.Context, st Store) (store.Thresholds, error) {
	rows, err := QueryOnce(ctx, st, thresholdsKeySQL(), nil)
	if err != nil {
		return store.Thresholds{}, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return th, err
	}
	return ApplyThresholdsFallback(th), nil
}
