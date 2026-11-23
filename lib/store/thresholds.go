package store

import (
	"fmt"
	"log"
	"math"
	"strconv"
)

type Thresholds struct {
	PendingMax    int
	DupSuspMax    int
	ConfFloor     float64
	StaleSessions int

	StaleDays   int
	StallHotH   int
	StallWriteH int

	SuggestMinScore        float64
	SuggestScanCap         int
	ConflictDupThreshold   float64
	BackupAgeWarnH         int
	ZeroNearMinRead        int
	ZeroNearWarnPct        int
	PendingAgeDays         int
	PendingPromoteDays     int
	PendingPromoteMinRunes int
	InvalidatedPruneDays   int

	RecallWarnWrites int

	DeadDays             int
	PairWindowMin        int
	ChainWindowH         int
	EventsRetentionD     int
	HotRetentionD        int
	EventsVolumeWarn     int
	DrillMaxDays         int
	DailyRetentionD      int
	RrfK                 float64
	SecretsScanRows      int
	SessionKnowledgeDays int

	DiagHotBudgetHigh float64
	DiagHotBudgetLow  float64
	DiagTrendPct      int
	DiagRecallLedGoal int
	DiagDeadWarn      int
	KreadsRetentionD  int

	ExportImportMaxRows int

	DecayRate    float64
	ArchiveBelow float64

	RecallEventRetentionD int

	CorrectionConfidence float64

	CorrectionDailyMax int

	GapQueryThreshold int
	GapDailyMax       int

	StaleScoreThreshold int
	StaleTTLDays        int

	SurvivedDecayRelief float64

	EchoTopK       int
	EchoMinScore   float64
	EchoTopicMatch float64

	WeeklyRollupsRetentionD      int
	ThresholdSnapshotsRetentionD int
	MeteringFreshnessWarnH       int

	VaultScanMaxFiles    int
	VaultSummaryMaxRunes int
	VaultSkipDirs        string

	DistillMinScore float64

	DistillMaxRunes int

	RecallAuditMinBad int

	DoltLogDepthWarn int
	DbSizeWarnMB     int

	UpdateCacheAgeH int
}

const (
	defaultPendingMax    = 50
	defaultDupSuspMax    = 20
	defaultConfFloor     = 0.8
	defaultStaleSessions = 3
	defaultStaleDays     = 90
	defaultStallHotH     = 48
	defaultStallWriteH   = 72

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

	defaultDiagHotBudgetHigh   = 0.8
	defaultDiagHotBudgetLow    = 0.2
	defaultDiagTrendPct        = 50
	defaultDiagRecallLedGoal   = 50
	defaultDiagDeadWarn        = 10
	defaultKreadsRetentionD    = 14
	defaultExportImportMaxRows = 100000

	defaultDecayRate    = 1.0
	defaultArchiveBelow = 0.0

	defaultRecallEventRetentionD = 90

	defaultCorrectionConfidence = 0.7

	defaultCorrectionDailyMax = 20

	defaultGapQueryThreshold = 3
	defaultGapDailyMax       = 10

	defaultStaleScoreThreshold = 70
	defaultStaleTTLDays        = 180

	defaultSurvivedDecayRelief = 0.0

	defaultEchoTopK       = 5
	defaultEchoMinScore   = 0.3
	defaultEchoTopicMatch = 0.4

	defaultWeeklyRollupsRetentionD      = 90
	defaultThresholdSnapshotsRetentionD = 90
	defaultMeteringFreshnessWarnH       = 336

	defaultVaultScanMaxFiles    = 10000
	defaultVaultSummaryMaxRunes = 200
	defaultVaultSkipDirs        = ".obsidian,.trash"

	defaultDistillMinScore = 1.0

	defaultDistillMaxRunes = 20000

	defaultRecallAuditMinBad = 2

	defaultDoltLogDepthWarn = 10000
	defaultDbSizeWarnMB     = 2048

	defaultUpdateCacheAgeH = 72
)

func DefaultThresholds() Thresholds {
	return Thresholds{
		PendingMax:    defaultPendingMax,
		DupSuspMax:    defaultDupSuspMax,
		ConfFloor:     defaultConfFloor,
		StaleSessions: defaultStaleSessions,
		StaleDays:     defaultStaleDays,
		StallHotH:     defaultStallHotH,
		StallWriteH:   defaultStallWriteH,

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

		DiagHotBudgetHigh: defaultDiagHotBudgetHigh,
		DiagHotBudgetLow:  defaultDiagHotBudgetLow,
		DiagTrendPct:      defaultDiagTrendPct,
		DiagRecallLedGoal: defaultDiagRecallLedGoal,
		DiagDeadWarn:      defaultDiagDeadWarn,
		KreadsRetentionD:  defaultKreadsRetentionD,

		ExportImportMaxRows: defaultExportImportMaxRows,

		DecayRate:    defaultDecayRate,
		ArchiveBelow: defaultArchiveBelow,

		RecallEventRetentionD: defaultRecallEventRetentionD,

		CorrectionConfidence: defaultCorrectionConfidence,
		CorrectionDailyMax:   defaultCorrectionDailyMax,

		GapQueryThreshold: defaultGapQueryThreshold,
		GapDailyMax:       defaultGapDailyMax,

		StaleScoreThreshold: defaultStaleScoreThreshold,
		StaleTTLDays:        defaultStaleTTLDays,

		SurvivedDecayRelief: defaultSurvivedDecayRelief,

		EchoTopK:       defaultEchoTopK,
		EchoMinScore:   defaultEchoMinScore,
		EchoTopicMatch: defaultEchoTopicMatch,

		WeeklyRollupsRetentionD:      defaultWeeklyRollupsRetentionD,
		ThresholdSnapshotsRetentionD: defaultThresholdSnapshotsRetentionD,
		MeteringFreshnessWarnH:       defaultMeteringFreshnessWarnH,

		VaultScanMaxFiles:    defaultVaultScanMaxFiles,
		VaultSummaryMaxRunes: defaultVaultSummaryMaxRunes,
		VaultSkipDirs:        defaultVaultSkipDirs,

		DistillMinScore: defaultDistillMinScore,

		DistillMaxRunes: defaultDistillMaxRunes,

		RecallAuditMinBad: defaultRecallAuditMinBad,

		DoltLogDepthWarn: defaultDoltLogDepthWarn,
		DbSizeWarnMB:     defaultDbSizeWarnMB,

		UpdateCacheAgeH: defaultUpdateCacheAgeH,
	}
}

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
			return th, fmt.Errorf("thresholds: key is not a string: %v", key)
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
			th.DrillMaxDays = parseIntOrDefault(val, defaultDrillMaxDays, ks)
		case "daily_retention_days":
			th.DailyRetentionD = parseIntOrDefault(val, defaultDailyRetentionD, ks)
		case "rrf_k":

			th.RrfK = parseFloatPositive(val, defaultRrfK, ks)
		case "secrets_scan_row_limit":
			th.SecretsScanRows = parseIntOrDefault(val, defaultSecretsScanRows, ks)
		case "session_knowledge_days":

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
			th.KreadsRetentionD = parseIntOrDefault(val, defaultKreadsRetentionD, ks)
		case "export_import_max_rows":

			th.ExportImportMaxRows = parseIntOrDefault(val, defaultExportImportMaxRows, ks)
		case "decay_rate":

			th.DecayRate = parseFloatPositive(val, defaultDecayRate, ks)
		case "archive_below":

			th.ArchiveBelow = parseFloatNonNegative(val, defaultArchiveBelow, ks)
		case "recall_event_retention_days":

			th.RecallEventRetentionD = parseIntOrDefault(val, defaultRecallEventRetentionD, ks)
		case "correction_confidence":

			th.CorrectionConfidence = parseFloatOrDefault(val, defaultCorrectionConfidence, ks)
		case "correction_daily_max":

			th.CorrectionDailyMax = parseIntOrDefault(val, defaultCorrectionDailyMax, ks)
		case "gap_query_threshold":

			th.GapQueryThreshold = parseIntOrDefault(val, defaultGapQueryThreshold, ks)
		case "gap_daily_max":

			th.GapDailyMax = parseIntOrDefault(val, defaultGapDailyMax, ks)
		case "stale_score_threshold":

			th.StaleScoreThreshold = parseIntOrDefault(val, defaultStaleScoreThreshold, ks)
		case "stale_ttl_days":

			th.StaleTTLDays = parseIntOrDefault(val, defaultStaleTTLDays, ks)
		case "survived_decay_relief":

			th.SurvivedDecayRelief = parseFloatNonNegative(val, defaultSurvivedDecayRelief, ks)
		case "echo_top_k":

			th.EchoTopK = parseIntOrDefault(val, defaultEchoTopK, ks)
		case "echo_min_score":

			th.EchoMinScore = parseFloatOrDefault(val, defaultEchoMinScore, ks)
		case "echo_topic_match":

			th.EchoTopicMatch = parseFloatOrDefault(val, defaultEchoTopicMatch, ks)
		case "weekly_rollups_retention_days":

			th.WeeklyRollupsRetentionD = parseIntOrDefault(val, defaultWeeklyRollupsRetentionD, ks)
		case "threshold_snapshots_retention_days":

			th.ThresholdSnapshotsRetentionD = parseIntOrDefault(val, defaultThresholdSnapshotsRetentionD, ks)
		case "metering_freshness_warn_h":

			th.MeteringFreshnessWarnH = parseIntOrDefault(val, defaultMeteringFreshnessWarnH, ks)
		case "vault_scan_max_files":

			th.VaultScanMaxFiles = parseIntOrDefault(val, defaultVaultScanMaxFiles, ks)
		case "vault_summary_max_runes":

			th.VaultSummaryMaxRunes = parseIntOrDefault(val, defaultVaultSummaryMaxRunes, ks)
		case "vault_skip_dirs":

			th.VaultSkipDirs = parseStringOrDefault(val, defaultVaultSkipDirs, ks)
		case "distill_min_score":

			th.DistillMinScore = parseFloatNonNegative(val, defaultDistillMinScore, ks)
		case "distill_max_runes":

			th.DistillMaxRunes = parseIntOrDefault(val, defaultDistillMaxRunes, ks)
		case "recall_audit_min_bad":

			th.RecallAuditMinBad = parseIntOrDefault(val, defaultRecallAuditMinBad, ks)
		case "dolt_" + "log_depth_warn":

			th.DoltLogDepthWarn = parseIntOrDefault(val, defaultDoltLogDepthWarn, ks)
		case "db_size_warn_mb":

			th.DbSizeWarnMB = parseIntOrDefault(val, defaultDbSizeWarnMB, ks)
		case "update_cache_age_h":

			th.UpdateCacheAgeH = parseIntOrDefault(val, defaultUpdateCacheAgeH, ks)
		}
	}
	return th, nil
}

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

func parseIntOrDefault(val any, def int, key string) int {
	return parseIntInRangeOrDefault(val, def, 0, key)
}

func parseIntInRangeOrDefault(val any, def, min int, key string) int {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s value type mismatch (%v) — default %d", key, val, def)
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		log.Printf("[flint] thresholds: cannot parse %s=%q (minimum %d) — default %d", key, s, min, def)
		return def
	}
	return n
}

func parseFloatPositive(val any, def float64, key string) float64 {
	s := fmt.Sprint(val)
	f, err := strconv.ParseFloat(s, 64)

	if err != nil || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		log.Printf("[flint] thresholds: cannot parse %s=%q (positive required) — default %v", key, s, def)
		return def
	}
	return f
}

func parseStringOrDefault(val any, def string, key string) string {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s value type mismatch (%v) — default %s", key, val, def)
		return def
	}
	return s
}

func parseFloatOrDefault(val any, def float64, key string) float64 {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s value type mismatch (%v) — default %v", key, val, def)
		return def
	}
	f, err := strconv.ParseFloat(s, 64)

	if err != nil || f < 0 || f > 1 || math.IsNaN(f) {
		log.Printf("[flint] thresholds: cannot parse %s=%q — default %v", key, s, def)
		return def
	}
	return f
}

func parseFloatNonNegative(val any, def float64, key string) float64 {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s value type mismatch (%v) — default %v", key, val, def)
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		log.Printf("[flint] thresholds: cannot parse %s=%q (non-negative required) — default %v", key, s, def)
		return def
	}
	return f
}
