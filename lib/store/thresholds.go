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
	SuggestMinScore        float64 // 제안(did-you-mean) 채택 유사도 하한
	SuggestScanCap         int     // 제안 스캔 행 상한(무상한 스캔 방어)
	ConflictDupThreshold   float64 // 모순/중복 분류 유사도 기준(이상=중복)
	BackupAgeWarnH         int     // 백업 신선도 경고 시간(doctor backup-age)
	ZeroNearMinRead        int     // zero_near 판정 표본 하한(doctor)
	ZeroNearWarnPct        int     // zero_near 경고 비율 %(doctor)
	PendingAgeDays         int     // pending 만료 안내 기준일(doctor)
	PendingPromoteDays     int     // pending 승격 추천 근사: 최근 N일(v0.29.0 R1)
	PendingPromoteMinRunes int     // 승격 추천 본문 최소 룬 수(v0.29.0 R1)
	InvalidatedPruneDays   int     // invalidated 정리 기준일(v0.33.0 R4)
	// v0.18.0 R3: done 회상 경고 — 오늘 회상 0회 상태의 기록 수 임계.
	RecallWarnWrites int // 이상이면 "회상 없이 기록만" 경고(done)
	// v0.20.4(패치 루프 4차·마일스톤 B③·C①): 효과 계측 판정 창·보존.
	DeadDays             int     // dead_pct 기준일(기본 30)
	PairWindowMin        int     // read→write 페어 판정 창(분, 기본 30)
	ChainWindowH         int     // 사용 연쇄 판정 창(시간, 기본 24)
	EventsRetentionD     int     // read_events·session_starts 보존(일, 기본 14)
	HotRetentionD        int     // hot_events 보존(일, 기본 30 — dead 가드 창 정렬)
	EventsVolumeWarn     int     // doctor 계측 테이블 경고 행 수(기본 50000)
	DrillMaxDays         int     // 드릴 경과 보고 기준일(기본 30, v0.23.0 이관)
	DailyRetentionD      int     // daily:* 보존·기간화 창 상한(기본 14, v0.24.0 R1b)
	RrfK                 float64 // 검색 RRF 순위 완화 계수(기본 60, v0.25.0)
	SecretsScanRows      int     // doctor secrets 1회 스캔 행 상한(기본 10000, v0.26.1)
	SessionKnowledgeDays int     // 세션 지식화 최소 간격(일, 기본 1 — v0.27.0. -1=끔)
	// v1.2.0 T5 — 규칙 진단 D 임계값(spec §3 diag_*·TUNING §2). B계층은
	// doctor 기존 키(zero_near_warn_pct·pending_age_days)를 승계 읽기한다
	// (중복 키 방지 — 신규 키는 A/C계층 자기참조·가치 기본값만).
	DiagHotBudgetHigh float64 // hot 주입 평균/예산 '만성 절단' 판정 상한(기본 0.8)
	DiagHotBudgetLow  float64 // hot 주입 평균/예산 정보성(여유) 판정 하한(기본 0.2)
	DiagTrendPct      int     // hot bytes 추세 급변 판정 %(기본 50)
	DiagRecallLedGoal int     // 회상 선도 세션 '기본 목표' %(C계층 가치 기본값, 기본 50)
	DiagDeadWarn      int     // dead_pct 진단 경고 %(기본 10)
	// v1.2.0 T5 — knowledge_reads 보존일 등록(T1 stopgap 인계 해소: 기존
	// per-key 직접 읽기와 동일 키·기본값 14, spec §1.2).
	KreadsRetentionD int // knowledge_reads 보존(일, 기본 14)
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
	defaultSuggestMinScore        = 0.15
	defaultSuggestScanCap         = 5000
	defaultConflictDupThreshold   = 0.6
	defaultBackupAgeWarnH         = 72
	defaultZeroNearMinRead        = 10
	defaultZeroNearWarnPct        = 30
	defaultPendingAgeDays         = 7
	defaultPendingPromoteDays     = 3
	defaultPendingPromoteMinRunes = 40
	defaultInvalidatedPruneDays   = 90
	defaultRecallWarnWrites       = 3
	// v0.20.4 — 효과 계측 판정 창·보존 기본값.
	defaultDeadDays             = 30
	defaultPairWindowMin        = 30
	defaultChainWindowH         = 24
	defaultEventsRetentionD     = 14
	defaultHotRetentionD        = 30
	defaultEventsVolumeWarn     = 50000
	defaultDrillMaxDays         = 30
	defaultDailyRetentionD      = 14
	defaultRrfK                 = 60.0
	defaultSecretsScanRows      = 10000
	defaultSessionKnowledgeDays = 1
	// v1.2.0 T5 — 진단 diag_*·knowledge_reads 보존 기본값(spec §3·§1.2).
	defaultDiagHotBudgetHigh = 0.8
	defaultDiagHotBudgetLow  = 0.2
	defaultDiagTrendPct      = 50
	defaultDiagRecallLedGoal = 50
	defaultDiagDeadWarn      = 10
	defaultKreadsRetentionD  = 14
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
		SuggestMinScore:        defaultSuggestMinScore,
		SuggestScanCap:         defaultSuggestScanCap,
		ConflictDupThreshold:   defaultConflictDupThreshold,
		BackupAgeWarnH:         defaultBackupAgeWarnH,
		ZeroNearMinRead:        defaultZeroNearMinRead,
		ZeroNearWarnPct:        defaultZeroNearWarnPct,
		PendingAgeDays:         defaultPendingAgeDays,
		PendingPromoteDays:     defaultPendingPromoteDays,
		PendingPromoteMinRunes: defaultPendingPromoteMinRunes,
		InvalidatedPruneDays:   defaultInvalidatedPruneDays,
		RecallWarnWrites:       defaultRecallWarnWrites,
		DeadDays:               defaultDeadDays,
		PairWindowMin:          defaultPairWindowMin,
		ChainWindowH:           defaultChainWindowH,
		EventsRetentionD:       defaultEventsRetentionD,
		HotRetentionD:          defaultHotRetentionD,
		EventsVolumeWarn:       defaultEventsVolumeWarn,
		DrillMaxDays:           defaultDrillMaxDays,
		DailyRetentionD:        defaultDailyRetentionD,
		RrfK:                   defaultRrfK,
		SecretsScanRows:        defaultSecretsScanRows,
		SessionKnowledgeDays:   defaultSessionKnowledgeDays,
		// v1.2.0 T5 진단 키 — 소비자(ops.Diagnose) 폴백 기준값.
		DiagHotBudgetHigh: defaultDiagHotBudgetHigh,
		DiagHotBudgetLow:  defaultDiagHotBudgetLow,
		DiagTrendPct:      defaultDiagTrendPct,
		DiagRecallLedGoal: defaultDiagRecallLedGoal,
		DiagDeadWarn:      defaultDiagDeadWarn,
		KreadsRetentionD:  defaultKreadsRetentionD,
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
		case "pending_promote_days":
			th.PendingPromoteDays = parseIntOrDefault(val, defaultPendingPromoteDays, ks)
		case "pending_promote_min_runes":
			th.PendingPromoteMinRunes = parseIntOrDefault(val, defaultPendingPromoteMinRunes, ks)
		case "invalidated_prune_days":
			th.InvalidatedPruneDays = parseIntOrDefault(val, defaultInvalidatedPruneDays, ks)
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
		case "rrf_k":
			// v1.2.1(R15-B1): rrf_k는 0~1 클램프 파서를 쓰면 기본값 60이
			// 전부 기각되어 오버라이드가 원천 불가했다(실증). 전용 파서로
			// 양수 전범위 허용 — RRF k는 통상 수십~수백(v0.25.0 스펙 참조).
			th.RrfK = parseFloatPositive(val, defaultRrfK, ks)
		case "secrets_scan_row_limit":
			th.SecretsScanRows = parseIntOrDefault(val, defaultSecretsScanRows, ks)
		case "session_knowledge_days":
			// -1=끔 센티넬(recall_warn_writes 관례 승계).
			th.SessionKnowledgeDays = parseIntInRangeOrDefault(val, defaultSessionKnowledgeDays, -1, ks)
		case "diag_hot_budget_high":
			th.DiagHotBudgetHigh = parseFloatOrDefault(val, defaultDiagHotBudgetHigh, ks)
		case "diag_hot_budget_low":
			th.DiagHotBudgetLow = parseFloatOrDefault(val, defaultDiagHotBudgetLow, ks)
		case "diag_trend_pct":
			th.DiagTrendPct = parseIntOrDefault(val, defaultDiagTrendPct, ks)
		case "diag_recall_led_goal":
			th.DiagRecallLedGoal = parseIntOrDefault(val, defaultDiagRecallLedGoal, ks)
		case "diag_dead_warn":
			th.DiagDeadWarn = parseIntOrDefault(val, defaultDiagDeadWarn, ks)
		case "knowledge_reads_retention_days":
			// v1.2.0 T5 등록(T1 stopgap 인계 — usage.go per-key 직접 읽기와
			// 동일 키·기본값. tuning/문서 노출은 등록표 후속 수렴).
			th.KreadsRetentionD = parseIntOrDefault(val, defaultKreadsRetentionD, ks)
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
// parseFloatPositive — 양수(>0) 허용 전용 float 파서. rrf_k(기본 60)처럼
// 0~1 범위 밖 값이 정상인 키용(v1.2.1 R15-B1).
func parseFloatPositive(val any, def float64, key string) float64 {
	s := fmt.Sprint(val)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가(양수 필요) — 기본값 %v", key, s, def)
		return def
	}
	return f
}

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
