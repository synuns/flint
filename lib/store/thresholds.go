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
			// v0.18.1: -1은 "끔"(recallStarvedToday 가드) — 공유 파서가
			// 음수를 기본값 코안다려 이 키만 -1까지 수용한다(0은 미설정과
			// 구분 불가라 loadThresholds가 기본값으로 폴백).
			if s, ok := toString(val); ok {
				if n, err := strconv.Atoi(s); err == nil && n >= -1 {
					th.RecallWarnWrites = n
					continue
				}
			}
			log.Printf("[flint] thresholds: %s=%v 파싱 불가 — 기본값 %d", ks, val, defaultRecallWarnWrites)
			th.RecallWarnWrites = defaultRecallWarnWrites
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
func parseIntOrDefault(val any, def int, key string) int {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s 값 타입 이상(%v) — 기본값 %d", key, val, def)
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가 — 기본값 %d", key, s, def)
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
