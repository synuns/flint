package wire

import (
	"context"
	"strings"

	"github.com/synuns/flint/lib/store"
)

func ThresholdsKeys() []string {
	metas := ThresholdMetas()
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Key)
	}
	return out
}

func thresholdsKeySQL() string {
	return "SELECT key, value FROM meta WHERE key IN ('" +
		strings.Join(ThresholdsKeys(), "','") + "')"
}

func ApplyThresholdsFallback(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	for _, f := range []struct {
		cur   *int
		def   int
		allow bool
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
		{&th.SessionKnowledgeDays, def.SessionKnowledgeDays, true},
		{&th.DailyRetentionD, def.DailyRetentionD, false},
		{&th.RecallEventRetentionD, def.RecallEventRetentionD, false},
		{&th.CorrectionDailyMax, def.CorrectionDailyMax, false},

		{&th.DoltLogDepthWarn, def.DoltLogDepthWarn, false},
		{&th.DbSizeWarnMB, def.DbSizeWarnMB, false},

		{&th.UpdateCacheAgeH, def.UpdateCacheAgeH, false},
	} {
		if *f.cur == 0 || (!f.allow && *f.cur < 0) {
			*f.cur = f.def
		}
	}
	if th.SuggestMinScore <= 0 {
		th.SuggestMinScore = def.SuggestMinScore
	}

	if th.CorrectionConfidence <= 0 {
		th.CorrectionConfidence = def.CorrectionConfidence
	}
	if th.ConflictDupThreshold <= 0 {
		th.ConflictDupThreshold = def.ConflictDupThreshold
	}
	return th
}

var LoadThresholdsFn = LoadThresholds

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
