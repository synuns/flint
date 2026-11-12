// thresholds.go — v0.39.0 T1(§1): loadThresholds 체계와 read 계측 wiring을
// lib/cli(maintain.go·metrics.go)에서 계약층으로 이동. knowledge 패키지
// 분리(v0.39.0)의 공용 소비 표면 — cli 잔류 파일은 cliwire_alias.go var
// 별칭으로 무편집 컴파일된다. 임계값 키 목록·폴백 규칙(미설정 키는
// DefaultThresholds)·read_stats 단일 라이터 계약은 이동 전과 동일하다.
package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/synuns/flint/lib/store"
)

// ThresholdsKeys — thresholds 메타 키 결정적 목록(v0.23.0 리뷰 ⑧: SQL·
// tuningKeys 3중 유지를 공유 소스로 — 드리프트 테스트가 이 리스트와
// tuningKeys의 동질성을 단언한다). cli maintain.go 이동분.
func ThresholdsKeys() []string {
	return []string{
		"pending_max", "dup_susp_max", "conf_floor", "stale_sessions",
		"stale_days", "stall_hot_h", "stall_write_h",
		"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
		"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
		"pending_age_days", "pending_promote_days", "pending_promote_min_runes", "invalidated_prune_days", "recall_warn_writes",
		"dead_days", "pair_window_minutes", "chain_window_hours",
		"events_retention_days", "hot_events_retention_days",
		"events_volume_warn", "drill_max_days", "daily_retention_days", "rrf_k", "secrets_scan_row_limit", "session_knowledge_days",
	}
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

// readStatsJSON — meta read_stats 영속 포맷(T56 제로히트 계측).
// zero_near는 v0.14.0 additive 추가 — 기존 blobs은 0 디폴트 디코딩,
// 0이면 생략(omitempty)해 BumpReadStats 저장 바이트는 불변으로 유지.
type readStatsJSON struct {
	Calls    int `json:"calls"`
	Zero     int `json:"zero"`
	ZeroNear int `json:"zero_near,omitempty"`
}

// loadReadStatsJSON — meta read_stats를 전체 구조체로 로드(v0.14.0 내부
// 파서 — zero_near 보존용). 키 부재·JSON 파손은 기본값 폴백(ok=false, 제로값).
// getKV 전송 오류는 err로 구분해 반환한다 — 호출부(Bump*)는 오류 시 setKV를
// 생략해 기존 calls/zero를 와이프하지 않는다.
func loadReadStatsJSON(getKV func(string) (string, error)) (readStatsJSON, bool, error) {
	v, err := getKV("read_stats")
	if err != nil {
		return readStatsJSON{}, false, err
	}
	if v == "" {
		return readStatsJSON{}, false, nil
	}
	var st readStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return readStatsJSON{}, false, nil // 파손 → 기본값 폴백
	}
	return st, true, nil
}

// LoadReadStats — meta read_stats 로드. 키 부재·오류·JSON 파손 시 기본값
// 폴백(ok=false, 0/0). cli metrics.go 이동분.
func LoadReadStats(getKV func(string) (string, error)) (calls int, zero int, ok bool) {
	st, ok, _ := loadReadStatsJSON(getKV)
	return st.Calls, st.Zero, ok
}

// BumpReadStats — read 호출 1회 누적(v0.15.0 R1 단일 라이터): read_stats를
// 한 번 읽어 calls/zero/zero_near를 전부 갱신해 저장한다(왕복 1회).
// 기존 read_stats는 getKV로 읽어 합산 후 setKV로 저장(hot_stats 패턴).
// 파손·부재는 0에서 재기산한다. getKV 전송 오류는 얼리리턴(재기작하면
// 읽지 못한 기존 값을 와이프한다). 이번 호출에 해당하지 않는 지표는
// 기존값을 보존한다(보존값 0이면 omitempty로 바이트 불변).
// cli metrics.go 이동분.
func BumpReadStats(zeroHits bool, zeroNear bool, getKV func(string) (string, error), setKV func(string, string) error) error {
	st, _, err := loadReadStatsJSON(getKV) // 파손·부재 폴백 후 재누적
	if err != nil {
		return err
	}
	st.Calls++
	if zeroHits {
		st.Zero++
	}
	if zeroNear {
		st.ZeroNear++
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return setKV("read_stats", string(b))
}

// BumpReadStatsToMeta — read 계측 wiring(v0.15.0 R1 단일 라이터): zero 히트
// 여부·제로근접 여부를 meta read_stats 1회 쓰기에 누적. 실패는 stderr 1행
// 이하로 조용히 무시(비간섭 §13). cli metrics.go 이동분.
func BumpReadStatsToMeta(ctx context.Context, st Store, zeroHits, zeroNear bool) {
	getKV := func(key string) (string, error) {
		v, ok, err := GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return "", err
		}
		return v, nil
	}
	setKV := func(key, value string) error {
		return SetMetaKV(ctx, st, key, value)
	}
	if err := BumpReadStats(zeroHits, zeroNear, getKV, setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] read stats persist: %v\n", err)
	}
}
