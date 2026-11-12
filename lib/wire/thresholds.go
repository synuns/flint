// thresholds.go — v0.39.0 T1(§1): loadThresholds 체계와 read 계측 wiring을
// lib/cli(maintain.go·metrics.go)에서 계약층으로 이동. knowledge 패키지
// 분리(v0.39.0)의 공용 소비 표면 — cli 잔류 파일은 cliwire_alias.go var
// 별칭으로 무편집 컴파일된다. 임계값 키 목록·폴백 규칙(미설정 키는
// DefaultThresholds)은 이동 전과 동일하다.
// v0.42.0 T2(§1): read_stats blob 계측(LoadReadStats·BumpReadStats·
// readStatsJSON)은 폐지 — read 계측 원천은 read_events(RecordReadEvent)와
// daily:* 파생 산출로 이전했다. 운영 DB에 잔존한 meta read_stats 키는
// 이후 읽지 않는다(무해 잔류 — 정리 원하면 psql에서 삭제).
package wire

import (
	"context"
	"strings"

	"github.com/synuns/flint/lib/store"
)

// ThresholdsKeys — thresholds 메타 키 결정적 목록. v0.42.0 T1(§2):
// threshold_meta.go 등록표에서 파생된다(키 집합·순서는 이전 하드코딩
// 목록과 동일 — 가드 테스트가 단언). SQL IN·maintain meta 감사·tuningKeys
// 동질성 드리프트 테스트의 공유 소스 계약은 불변.
func ThresholdsKeys() []string {
	metas := ThresholdMetas()
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Key)
	}
	return out
}

// thresholdsKeySQL — IN 목록 SQL 조각(결정적 순서).
func thresholdsKeySQL() string {
	return "SELECT key, value FROM meta WHERE key IN ('" +
		strings.Join(ThresholdsKeys(), "','") + "')"
}

// ApplyThresholdsFallback — 미설정(0)·비허용 음수 키를 DefaultThresholds
// 값으로 폴백(ParseThresholds 미설정-0 규약의 소비자 측 완결). cli
// maintain.go 이동분 — tuning.go 등 cli 잔류 소비자는 별칭으로 승계.
func ApplyThresholdsFallback(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	for _, f := range []struct {
		cur   *int
		def   int
		allow bool // true면 -1 등 음수 센티널 허용 키(RecallWarnWrites)
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
		{&th.SessionKnowledgeDays, def.SessionKnowledgeDays, true}, // -1=끔 센티널 허용
		{&th.DailyRetentionD, def.DailyRetentionD, false},
	} {
		if *f.cur == 0 || (!f.allow && *f.cur < 0) {
			*f.cur = f.def
		}
	}
	if th.SuggestMinScore <= 0 {
		th.SuggestMinScore = def.SuggestMinScore
	}
	if th.ConflictDupThreshold <= 0 {
		th.ConflictDupThreshold = def.ConflictDupThreshold
	}
	return th
}

// LoadThresholdsFn — LoadThresholds seam(v0.25.1 패치 루프: rrf_k 주입
// 순서 테스트용 주입점). cli 하위 패키지 테스트는 cliwire_alias var
// 별칭(loadThresholdsFn)으로 기존 교체 관례를 승계한다.
var LoadThresholdsFn = LoadThresholds

// LoadThresholds — meta 테이블 키-값 → ParseThresholds(미설정 키 기본값).
// v0.6 키(stale_days·stall_hot_h·stall_write_h)와 v0.16.3 키(suggest_*·
// conflict_dup_threshold·backup_age_warn_h·zero_near_*·pending_age_days)는
// ParseThresholds가 미설정을 0으로 두므로(기존 테스트 호환 규약)
// DefaultThresholds 값으로 폴백한다. 조회 실패는 err 전파 — 호출부(cli
// doctor 등)가 DefaultThresholds로 폴백하는 기존 관례와 동일. cli
// maintain.go 이동분.
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
