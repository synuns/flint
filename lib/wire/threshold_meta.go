// threshold_meta.go — v0.42.0 T1(§2): thresholds 키 등록표(단일 소스).
// 키 추가는 이 등록표 1곳 + store.Thresholds 필드 정의로 수렴한다 —
// ThresholdsKeys(thresholdsKeySQL IN·maintain meta 감사)와 ops
// TuningKeys(TUNING §2 표 행: Default·Consumer·Get)가 전부 등록표에서
// 파생된다. DefaultThresholds·ParseThresholds 값·인식 집합은
// threshold_meta_test.go가 등록표와의 동질성을 가드한다(한쪽만 고치면
// FAIL — 스펙 §2 가드 계약). store는 구조체(필드명)만 공유한다.
package wire

// ThresholdMeta — 등록표 항목. Field는 store.Thresholds 필드명(파생 Get의
// 리플렉션 소스), Key는 meta 오버라이드 키, Default는 기본값(int|float64
// — store 기본 상수와 동질 가드 대상), Desc는 소비 경로(TUNING §2 표의
// consumer 열).
type ThresholdMeta struct {
	Field   string
	Key     string
	Default any
	Desc    string
}

// thresholdMetas — 등록표 원본. 순서는 기존 ThresholdsKeys·TuningKeys
// 선언 순서를 그대로 보존한다(결정적 순서 — SQL IN·표 출력 불변).
var thresholdMetas = []ThresholdMeta{
	{"PendingMax", "pending_max", 50, "maintain --check"},
	{"DupSuspMax", "dup_susp_max", 20, "maintain --check"},
	{"ConfFloor", "conf_floor", 0.8, "write 판정"},
	{"StaleSessions", "stale_sessions", 3, "done"},
	{"StaleDays", "stale_days", 90, "maintain --stale·report"},
	{"StallHotH", "stall_hot_h", 48, "doctor·maintain"},
	{"StallWriteH", "stall_write_h", 72, "doctor·maintain"},
	{"SuggestMinScore", "suggest_min_score", 0.15, "read 제안"},
	{"SuggestScanCap", "suggest_scan_cap", 5000, "read 제안"},
	{"ConflictDupThreshold", "conflict_dup_threshold", 0.6, "maintain 분류"},
	{"BackupAgeWarnH", "backup_age_warn_h", 72, "doctor backup-age"},
	{"ZeroNearMinRead", "zero_near_min_read", 10, "doctor zero-near"},
	{"ZeroNearWarnPct", "zero_near_warn_pct", 30, "doctor zero-near"},
	{"PendingAgeDays", "pending_age_days", 7, "doctor pending-age"},
	{"PendingPromoteDays", "pending_promote_days", 3, "pending 승격 추천(최근 N일)"},
	{"PendingPromoteMinRunes", "pending_promote_min_runes", 40, "pending 승격 추천(본문 최소 룬)"},
	{"InvalidatedPruneDays", "invalidated_prune_days", 90, "invalidated 정리 기준일(maintain --prune-invalidated)"},
	{"RecallWarnWrites", "recall_warn_writes", 3, "done 회상 경고"},
	{"DeadDays", "dead_days", 30, "report effect"},
	{"PairWindowMin", "pair_window_minutes", 30, "report effect"},
	{"ChainWindowH", "chain_window_hours", 24, "report effect"},
	{"EventsRetentionD", "events_retention_days", 14, "계측 정리"},
	{"HotRetentionD", "hot_events_retention_days", 30, "계측 정리"},
	{"EventsVolumeWarn", "events_volume_warn", 50000, "doctor event-volume"},
	{"DrillMaxDays", "drill_max_days", 30, "maintain --check"},
	{"DailyRetentionD", "daily_retention_days", 14, "보존·report/status 창"},
	{"RrfK", "rrf_k", 60.0, "read 폴백 재랭킹"},
	{"SecretsScanRows", "secrets_scan_row_limit", 10000, "doctor secrets 행 상한"},
	{"SessionKnowledgeDays", "session_knowledge_days", 1, "세션 지식화 최소 간격"},
}

// ThresholdMetas — 등록표 결정적 복사본(29항목 — 키 집합은 기존
// ThresholdsKeys와 동일, 가드 테스트가 단언).
func ThresholdMetas() []ThresholdMeta {
	return append([]ThresholdMeta(nil), thresholdMetas...)
}

// ThresholdMetasAppendForTest — 가드 테스트 전용 일시 등록 seam
// (TestNewKeyPropagates: 가상 키 1건 등록 → 파생 4경로 동시 반영 실증).
// 반환된 restore로 원본 등록표를 복원한다. 프로덕션 경로 사용 금지.
func ThresholdMetasAppendForTest(m ThresholdMeta) (restore func()) {
	saved := thresholdMetas
	next := append([]ThresholdMeta(nil), saved...)
	thresholdMetas = append(next, m)
	return func() { thresholdMetas = saved }
	// v1.2.0 T5: 진단 엔진(diagnosis.go)·knowledge_reads 보존 — meta
	// 오버라이드 실로드를 위한 등록표 항목(T5 concern 수렴).
	{"DiagHotBudgetHigh", "diag_hot_budget_high", 0.8, "report 진단"},
	{"DiagHotBudgetLow", "diag_hot_budget_low", 0.2, "report 진단"},
	{"DiagTrendPct", "diag_trend_pct", 50, "report 진단"},
	{"DiagRecallLedGoal", "diag_recall_led_goal", 50, "report 진단"},
	{"DiagDeadWarn", "diag_dead_warn", 10, "report 진단"},
	{"KnowledgeReadsRetentionDays", "knowledge_reads_retention_days", 14, "db 정리"},

}
