package wire

type ThresholdMeta struct {
	Field   string
	Key     string
	Default any
	Desc    string
	Auto    string
}

var thresholdMetas = []ThresholdMeta{
	{"PendingMax", "pending_max", 50, "maintain --check", ""},
	{"DupSuspMax", "dup_susp_max", 20, "maintain --check", ""},
	{"ConfFloor", "conf_floor", 0.8, "write verdict", ""},
	{"StaleSessions", "stale_sessions", 3, "done", ""},
	{"StaleDays", "stale_days", 90, "maintain --stale·report", ""},
	{"StallHotH", "stall_hot_h", 48, "doctor·maintain", ""},
	{"StallWriteH", "stall_write_h", 72, "doctor·maintain", ""},
	{"SuggestMinScore", "suggest_min_score", 0.15, "read suggestions", "shadow"},
	{"SuggestScanCap", "suggest_scan_cap", 5000, "read suggestions", ""},
	{"ConflictDupThreshold", "conflict_dup_threshold", 0.6, "maintain classification", ""},
	{"BackupAgeWarnH", "backup_age_warn_h", 72, "doctor backup-age", ""},
	{"ZeroNearMinRead", "zero_near_min_read", 10, "doctor zero-near", ""},
	{"ZeroNearWarnPct", "zero_near_warn_pct", 30, "doctor zero-near", ""},
	{"PendingAgeDays", "pending_age_days", 7, "doctor pending-age", ""},
	{"PendingPromoteDays", "pending_promote_days", 3, "pending promote suggestion (recent N days)", ""},
	{"PendingPromoteMinRunes", "pending_promote_min_runes", 40, "pending promote suggestion (min body runes)", ""},
	{"InvalidatedPruneDays", "invalidated_prune_days", 90, "invalidated prune age (maintain --prune-invalidated)", ""},
	{"RecallWarnWrites", "recall_warn_writes", 3, "done recall warning", ""},
	{"DeadDays", "dead_days", 30, "report effect", ""},
	{"PairWindowMin", "pair_window_minutes", 30, "report effect", ""},
	{"ChainWindowH", "chain_window_hours", 24, "report effect", ""},
	{"EventsRetentionD", "events_retention_days", 14, "events retention", ""},
	{"HotRetentionD", "hot_events_retention_days", 30, "events retention", ""},
	{"EventsVolumeWarn", "events_volume_warn", 50000, "doctor event-volume", ""},
	{"DrillMaxDays", "drill_max_days", 30, "maintain --check", ""},
	{"DailyRetentionD", "daily_retention_days", 14, "retention · report/status window", ""},
	{"RrfK", "rrf_k", 60.0, "read fallback re-ranking", ""},
	{"SecretsScanRows", "secrets_scan_row_limit", 10000, "doctor secrets row limit", ""},
	{"SessionKnowledgeDays", "session_knowledge_days", 1, "session knowledge minimum interval", ""},

	{"DiagHotBudgetHigh", "diag_hot_budget_high", 0.8, "report diagnostics", ""},
	{"DiagHotBudgetLow", "diag_hot_budget_low", 0.2, "report diagnostics", ""},
	{"DiagTrendPct", "diag_trend_pct", 50, "report diagnostics", ""},
	{"DiagRecallLedGoal", "diag_recall_led_goal", 50, "report diagnostics", ""},
	{"DiagDeadWarn", "diag_dead_warn", 10, "report diagnostics", ""},
	{"KreadsRetentionD", "knowledge_reads_retention_days", 14, "db retention", ""},

	{"ExportImportMaxRows", "export_import_max_rows", 100000, "import row limit", ""},

	{"DecayRate", "decay_rate", 1.0, "maintain --decay-audit", ""},
	{"ArchiveBelow", "archive_below", 0.0, "maintain --decay-audit", ""},

	{"RecallEventRetentionD", "recall_event_retention_days", 90, "maintain --prune-invalidated (recall events retention)", ""},

	{"CorrectionConfidence", "correction_confidence", 0.7, "write correction source default", ""},

	{"CorrectionDailyMax", "correction_daily_max", 20, "write correction daily cap", ""},

	{"GapQueryThreshold", "gap_query_threshold", 3, "gap candidate accrual threshold", ""},
	{"GapDailyMax", "gap_daily_max", 10, "gap candidate daily cap", ""},

	{"StaleScoreThreshold", "stale_score_threshold", 70, "maintain --age-audit over-threshold marking", ""},
	{"StaleTTLDays", "stale_ttl_days", 180, "maintain --age-audit score TTL saturation", ""},

	{"SurvivedDecayRelief", "survived_decay_relief", 0.0, "survived decay relief (hot·brief·decay-audit)", ""},

	{"EchoTopK", "echo_top_k", 5, "write echo top-k cap", ""},
	{"EchoMinScore", "echo_min_score", 0.3, "write echo admission floor", ""},
	{"EchoTopicMatch", "echo_topic_match", 0.4, "write echo topic-overlap floor", ""},

	{"WeeklyRollupsRetentionD", "weekly_rollups_retention_days", 90, "rollup retention", ""},
	{"ThresholdSnapshotsRetentionD", "threshold_snapshots_retention_days", 90, "snapshot retention", ""},
	{"MeteringFreshnessWarnH", "metering_freshness_warn_h", 336, "doctor metering-freshness", ""},

	{"VaultScanMaxFiles", "vault_scan_max_files", 10000, "index vault scan file cap", ""},
	{"VaultSummaryMaxRunes", "vault_summary_max_runes", 200, "index vault summary truncation runes", ""},
	{"VaultSkipDirs", "vault_skip_dirs", ".obsidian,.trash", "index vault skip dirs (comma list)", ""},

	{"DistillMinScore", "distill_min_score", 1.0, "distill output minimum recall count", ""},
	{"DistillMaxRunes", "distill_max_runes", 20000, "distill output file rune cap", ""},

	{"RecallAuditMinBad", "recall_audit_min_bad", 2, "maintain --recall-audit listing floor", ""},

	{"DoltLogDepthWarn", "dolt_" + "log_depth_warn", 10000, "doctor depth warning", ""},
	{"DbSizeWarnMB", "db_size_warn_mb", 2048, "doctor db_size", ""},

	{"UpdateCacheAgeH", "update_cache_age_h", 72, "doctor versions cache age", ""},
}

func ThresholdMetas() []ThresholdMeta {
	return append([]ThresholdMeta(nil), thresholdMetas...)
}

func ThresholdAutoGrade(key string) string {
	for _, m := range thresholdMetas {
		if m.Key == key {
			return m.Auto
		}
	}
	return ""
}

func ThresholdAutoWhitelist() map[string]string {
	out := map[string]string{}
	for _, m := range thresholdMetas {
		if m.Auto != "" {
			out[m.Key] = m.Auto
		}
	}
	return out
}

func ThresholdMetasAppendForTest(m ThresholdMeta) (restore func()) {
	saved := thresholdMetas
	next := append([]ThresholdMeta(nil), saved...)
	thresholdMetas = append(next, m)
	return func() { thresholdMetas = saved }

}
