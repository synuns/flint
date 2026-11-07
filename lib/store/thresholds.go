package store

import (
	"fmt"
	"log"
	"strconv"
)

// Thresholds는 meta 테이블에서 읽은 임계값 상수(전역 제약: 하드코딩 금지).
type Thresholds struct {
	PendingMax    int
	DupSuspMax    int
	ConfFloor     float64
	StaleSessions int
	// v0.6(T66): stale 스캔·stall 진단 임계값.
	StaleDays   int // maintain --stale 기준일(Confluence 관례 90)
	StallHotH   int // last_hot_at stall grace 시간(Healthchecks.io 준용 48)
	StallWriteH int // last_write_at stall grace 시간(72)
	// v0.16.3: 하드코딩됐던 튜닝 상수의 레지스트리화(개인 튜닝값은
	// meta 오버라이드로만 — 코드·문서 커밋 금지 원칙).
	SuggestMinScore      float64 // 제안(did-you-mean) 채택 유사도 하한
	SuggestScanCap       int     // 제안 스캔 행 상한(무상한 스캔 방어)
	ConflictDupThreshold float64 // 모순/중복 분류 유사도 기준(이상=중복)
	BackupAgeWarnH       int     // 백업 신선도 경고 시간(doctor backup-age)
	ZeroNearMinRead      int     // zero_near 판정 표본 하한(doctor)
	ZeroNearWarnPct      int     // zero_near 경고 비율 %(doctor)
	PendingAgeDays       int     // pending 만료 안내 기준일(doctor)
	// v0.18.0 R3: done 회상 경고 — 오늘 회상 0회 상태의 기록 수 임계.
	RecallWarnWrites int // 이상이면 "회상 없이 기록만" 경고(done)
	// v0.20.4(패치 루프 4차·마일스톤 B③·C①): 효과 계측 판정 창·보존.
	DeadDays         int // dead_pct 기준일(기본 30)
	PairWindowMin    int // read→write 페어 판정 창(분, 기본 30)
	ChainWindowH     int // 사용 연쇄 판정 창(시간, 기본 24)
	EventsRetentionD int // read_events·session_starts 보존(일, 기본 14)
	HotRetentionD    int // hot_events 보존(일, 기본 30 — dead 가드 창 정렬)
	EventsVolumeWarn int // doctor 계측 테이블 경고 행 수(기본 50000)
	DrillMaxDays     int // 드릴 경과 보고 기준일(기본 30, v0.23.0 이관)
	DailyRetentionD  int // daily:* 보존·기간화 창 상한(기본 14, v0.24.0 R1b)
}

// 기본값 — pending>50·중복의심>20·conf<0.8·연속 3세션·stale 90일·stall 48/72h.
const (
	defaultPendingMax    = 50
	defaultDupSuspMax    = 20
	defaultConfFloor     = 0.8
	defaultStaleSessions = 3
	defaultStaleDays     = 90
	defaultStallHotH     = 48
	defaultStallWriteH   = 72
	// v0.16.3 — 소비부에서 제거한 하드코딩 상수의 기본값(단일 소스).
	defaultSuggestMinScore      = 0.15
	defaultSuggestScanCap       = 5000
	defaultConflictDupThreshold = 0.6
	defaultBackupAgeWarnH       = 72
	defaultZeroNearMinRead      = 10
	defaultZeroNearWarnPct      = 30
	defaultPendingAgeDays       = 7
	defaultRecallWarnWrites     = 3
	// v0.20.4 — 효과 계측 판정 창·보존 기본값.
	defaultDeadDays         = 30
	defaultPairWindowMin    = 30
	defaultChainWindowH     = 24
	defaultEventsRetentionD = 14
	defaultHotRetentionD    = 30
	defaultEventsVolumeWarn = 50000
	defaultDrillMaxDays     = 30
	defaultDailyRetentionD  = 14
)

// DefaultThresholds는 전 키 기본값 구조체.
func DefaultThresholds() Thresholds {
	return Thresholds{
		PendingMax:    defaultPendingMax,
		DupSuspMax:    defaultDupSuspMax,
		ConfFloor:     defaultConfFloor,
		StaleSessions: defaultStaleSessions,
		StaleDays:     defaultStaleDays,
		StallHotH:     defaultStallHotH,
		StallWriteH:   defaultStallWriteH,
		// v0.16.3 키 — 미설정 시 0을 유지하는 ParseThresholds와 달리
		// 여기는 항상 기본값(소비자 폴백 기준).
		SuggestMinScore:      defaultSuggestMinScore,
		SuggestScanCap:       defaultSuggestScanCap,
		ConflictDupThreshold: defaultConflictDupThreshold,
		BackupAgeWarnH:       defaultBackupAgeWarnH,
		ZeroNearMinRead:      defaultZeroNearMinRead,
		ZeroNearWarnPct:      defaultZeroNearWarnPct,
		PendingAgeDays:       defaultPendingAgeDays,
		RecallWarnWrites:     defaultRecallWarnWrites,
		DeadDays:             defaultDeadDays,
		PairWindowMin:        defaultPairWindowMin,
		ChainWindowH:         defaultChainWindowH,
		EventsRetentionD:     defaultEventsRetentionD,
		HotRetentionD:        defaultHotRetentionD,
		EventsVolumeWarn:     defaultEventsVolumeWarn,
		DrillMaxDays:         defaultDrillMaxDays,
		DailyRetentionD:      defaultDailyRetentionD,
	}
}

// ParseThresholds는 meta 테이블 키-값 읽기 결과(rows)를 Thresholds로 변환한다.
// v0.5 키는 미설정 시 기본값, 잘못된 값은 기본값+경고(에러 아님).
// v0.6 키(stale_days·stall_hot_h·stall_write_h)와 v0.16.3 키
// (suggest_min_score·suggest_scan_cap·conflict_dup_threshold·
// backup_age_warn_h·zero_near_min_read·zero_near_warn_pct·pending_age_days)는
// 미설정 시 0(미설정)을 유지한다 — 소비자(loadThresholds)가 DefaultThresholds
// 값으로 폴백한다. (기존 전체 구조체 비교 테스트의 회귀 없이 필드를
// 확장하기 위한 규약. 잘못된 값은 v0.5와 같이 기본값+경고.)
// e.g. ParseThresholds([][2]any{{"pending_max","50"},{"conf_floor","0.8"}}) -> {50,20,0.8,3,0,0,0}
func ParseThresholds(rows [][2]any) (Thresholds, error) {
	th := Thresholds{
		PendingMax:    defaultPendingMax,
		DupSuspMax:    defaultDupSuspMax,
		ConfFloor:     defaultConfFloor,
		StaleSessions: defaultStaleSessions,
	}
	for _, row := range rows {
		key, val := row[0], row[1]
		ks, ok := key.(string)
		if !ok {
			return th, fmt.Errorf("thresholds: 키가 문자열 아님: %v", key)
		}
		switch ks {
		case "pending_max":
			th.PendingMax = parseIntOrDefault(val, defaultPendingMax, ks)
		case "dup_susp_max":
			th.DupSuspMax = parseIntOrDefault(val, defaultDupSuspMax, ks)
		case "conf_floor":
			th.ConfFloor = parseFloatOrDefault(val, defaultConfFloor, ks)
		case "stale_sessions":
			th.StaleSessions = parseIntOrDefault(val, defaultStaleSessions, ks)
		case "stale_days":
			th.StaleDays = parseIntOrDefault(val, defaultStaleDays, ks)
		case "stall_hot_h":
			th.StallHotH = parseIntOrDefault(val, defaultStallHotH, ks)
		case "stall_write_h":
			th.StallWriteH = parseIntOrDefault(val, defaultStallWriteH, ks)
		case "suggest_min_score":
			th.SuggestMinScore = parseFloatOrDefault(val, defaultSuggestMinScore, ks)
		case "suggest_scan_cap":
			th.SuggestScanCap = parseIntOrDefault(val, defaultSuggestScanCap, ks)
		case "conflict_dup_threshold":
			th.ConflictDupThreshold = parseFloatOrDefault(val, defaultConflictDupThreshold, ks)
		case "backup_age_warn_h":
			th.BackupAgeWarnH = parseIntOrDefault(val, defaultBackupAgeWarnH, ks)
		case "zero_near_min_read":
			th.ZeroNearMinRead = parseIntOrDefault(val, defaultZeroNearMinRead, ks)
		case "zero_near_warn_pct":
			th.ZeroNearWarnPct = parseIntOrDefault(val, defaultZeroNearWarnPct, ks)
		case "pending_age_days":
			th.PendingAgeDays = parseIntOrDefault(val, defaultPendingAgeDays, ks)
		case "recall_warn_writes":
			// v0.18.2: min=-1 수용(-1=끔). 0은 미설정과 구분 불가 —
			// loadThresholds가 기본값으로 폴백한다(TUNING §2 문서화).
			th.RecallWarnWrites = parseIntInRangeOrDefault(val, defaultRecallWarnWrites, -1, ks)
		case "dead_days":
			th.DeadDays = parseIntOrDefault(val, defaultDeadDays, ks)
		case "pair_window_minutes":
			th.PairWindowMin = parseIntOrDefault(val, defaultPairWindowMin, ks)
		case "chain_window_hours":
			th.ChainWindowH = parseIntOrDefault(val, defaultChainWindowH, ks)
		case "events_retention_days":
			th.EventsRetentionD = parseIntOrDefault(val, defaultEventsRetentionD, ks)
		case "hot_events_retention_days":
			th.HotRetentionD = parseIntOrDefault(val, defaultHotRetentionD, ks)
		case "events_volume_warn":
			th.EventsVolumeWarn = parseIntOrDefault(val, defaultEventsVolumeWarn, ks)
		case "drill_max_days":
			// v0.23.0: drillReport가 직접 읽던 메타 키를 레지스트리로
			// 이관 — tuning 커맨드의 단일 소스.
			th.DrillMaxDays = parseIntOrDefault(val, defaultDrillMaxDays, ks)
		case "daily_retention_days":
			th.DailyRetentionD = parseIntOrDefault(val, defaultDailyRetentionD, ks)

		}
	}
	return th, nil
}

// toString은 any 값을 문자열로 정규화한다(숫자 타입 수용).
func toString(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
}

// parseIntOrDefault는 정수 파싱 실패 시 기본값+경고.
// parseIntOrDefault — 정수 파싱(하한 0: 음수는 기본값). v0.18.2: 하한
// 일반화 본체(parseIntInRangeOrDefault)의 별명.
func parseIntOrDefault(val any, def int, key string) int {
	return parseIntInRangeOrDefault(val, def, 0, key)
}

// parseIntInRangeOrDefault — min 이상만 수용(min 미만·파싱 불가는 기본값).
// 센티넬을 쓰는 키(예: recall_warn_writes -1=끔)는 min=-1로 확장한다.
func parseIntInRangeOrDefault(val any, def, min int, key string) int {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s 값 타입 이상(%v) — 기본값 %d", key, val, def)
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가(하한 %d) — 기본값 %d", key, s, min, def)
		return def
	}
	return n
}

// parseFloatOrDefault는 실수 파싱 실패 시 기본값+경고.
func parseFloatOrDefault(val any, def float64, key string) float64 {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s 값 타입 이상(%v) — 기본값 %v", key, val, def)
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1 {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가 — 기본값 %v", key, s, def)
		return def
	}
	return f
}
