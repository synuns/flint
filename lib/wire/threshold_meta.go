// threshold_meta.go — the thresholds key registry (the single source).
// Adding a key converges to this 1 registry spot + the store.Thresholds field
// definition — ThresholdsKeys (the thresholdsKeySQL IN and the maintain meta
// audit) and ops TuningKeys (the TUNING §2 table rows: Default, Consumer, Get)
// all derive from the registry. The DefaultThresholds values and the
// ParseThresholds recognition set are guarded for homogeneity with the registry
// by threshold_meta_test.go (fixing only one side FAILs — the spec §2 guard contract). store shares only the struct (the field names).
package wire

// ThresholdMeta — one registry entry. Field is the store.Thresholds field name
// (the reflection source of the derived Get), Key is the meta override key,
// Default is the default value (int|float64 — subject to the homogeneity guard
// against the store default constants), Desc is the consumption path (the consumer column of the TUNING §2 table),
// and Auto is the autonomous-tuning grade ("" = manual-only; "shadow" = the
// v1.23.0 shadow-circuit whitelist — the closed grade set guarded by
// threshold_meta_test.go).
type ThresholdMeta struct {
	Field   string
	Key     string
	Default any
	Desc    string
	Auto    string
}

// thresholdMetas — the registry original. The order preserves the existing
// ThresholdsKeys and TuningKeys declaration order as-is (a deterministic order — the SQL IN and table output unchanged).
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

	// the diagnostics engine (diagnosis.go) and the knowledge_reads retention — a
	// registry entry for the real meta-override load.
	{"DiagHotBudgetHigh", "diag_hot_budget_high", 0.8, "report diagnostics", ""},
	{"DiagHotBudgetLow", "diag_hot_budget_low", 0.2, "report diagnostics", ""},
	{"DiagTrendPct", "diag_trend_pct", 50, "report diagnostics", ""},
	{"DiagRecallLedGoal", "diag_recall_led_goal", 50, "report diagnostics", ""},
	{"DiagDeadWarn", "diag_dead_warn", 10, "report diagnostics", ""},
	{"KreadsRetentionD", "knowledge_reads_retention_days", 14, "db retention", ""},

	// the import row cap (a large-document memory defense — the consumption path knowledge/import_jsonl.go).
	// knowledge/import_jsonl.go).
	{"ExportImportMaxRows", "export_import_max_rows", 100000, "import row limit", ""},

	// the decay coefficient and archive-recommendation thresholds (the consumption path ops maintain --decay-audit — schema v11 recall_score, last_recalled_at).
	// maintain --decay-audit — schema v11 recall_score·last_recalled_at).
	{"DecayRate", "decay_rate", 1.0, "maintain --decay-audit", ""},
	{"ArchiveBelow", "archive_below", 0.0, "maintain --decay-audit", ""},

	// the recall_events retention (the growth cap of the schema v12 append-only
	// recall source — the consumption path: maintain --prune-invalidated event cleanup).
	{"RecallEventRetentionD", "recall_event_retention_days", 90, "maintain --prune-invalidated (recall events retention)", ""},

	// the --source correction write default confidence (the consumption path knowledge/write.go applyCorrectionDefaults).
	{"CorrectionConfidence", "correction_confidence", 0.7, "write correction source default", ""},

	// the --source correction daily accrual cap (the consumption path knowledge/write.go — the daily:correction_write flat-key count).
	{"CorrectionDailyMax", "correction_daily_max", 20, "write correction daily cap", ""},

	// the gap-candidate accrual trigger and daily cap (v1.12.0 — the consumption path knowledge/write.go gap source, the daily:gapq flat-key count).
	{"GapQueryThreshold", "gap_query_threshold", 3, "gap candidate accrual threshold", ""},
	{"GapDailyMax", "gap_daily_max", 10, "gap candidate daily cap", ""},

	// the stale-score report marking threshold and the TTL saturation days (v1.13.0 —
	// the consumption path ops maintain --age-audit·StaleScore).
	{"StaleScoreThreshold", "stale_score_threshold", 70, "maintain --age-audit over-threshold marking", ""},
	{"StaleTTLDays", "stale_ttl_days", 180, "maintain --age-audit score TTL saturation", ""},

	// the survived decay relief (v1.14.0 — the verified-success weighting in the
	// effective-score formula; the consumption path hot·brief ranking·maintain --decay-audit).
	{"SurvivedDecayRelief", "survived_decay_relief", 0.0, "survived decay relief (hot·brief·decay-audit)", ""},

	// the echo-scan keys (v1.17.0 — the consumption path knowledge write's echo
	// response: the top-k cap, the admission floor, and the topic-overlap floor).
	{"EchoTopK", "echo_top_k", 5, "write echo top-k cap", ""},
	{"EchoMinScore", "echo_min_score", 0.3, "write echo admission floor", ""},
	{"EchoTopicMatch", "echo_topic_match", 0.4, "write echo topic-overlap floor", ""},

	// the schema v13 observability keys (v1.19.0 — the consumption paths: the
	// rollup·snapshot retention prunes and the doctor metering-freshness check).
	{"WeeklyRollupsRetentionD", "weekly_rollups_retention_days", 90, "rollup retention", ""},
	{"ThresholdSnapshotsRetentionD", "threshold_snapshots_retention_days", 90, "snapshot retention", ""},
	{"MeteringFreshnessWarnH", "metering_freshness_warn_h", 336, "doctor metering-freshness", ""},

	// the vault-bridge keys (v1.21.0 — the schema v16 vault_files index:
	// the per-scan file cap, the summary truncation runes, and the
	// comma-separated skip-dir list — the first string-valued key).
	{"VaultScanMaxFiles", "vault_scan_max_files", 10000, "index vault scan file cap", ""},
	{"VaultSummaryMaxRunes", "vault_summary_max_runes", 200, "index vault summary truncation runes", ""},
	{"VaultSkipDirs", "vault_skip_dirs", ".obsidian,.trash", "index vault skip dirs (comma list)", ""},

	// the distill output floor (v1.22.0 — the schema v17 distill command:
	// the minimum raw recall count of an emitted insight row) and the
	// per-run file cap (v1.22.1 r97 — rows past the cap defer to the next
	// run).
	{"DistillMinScore", "distill_min_score", 1.0, "distill output minimum recall count", ""},
	{"DistillMaxRunes", "distill_max_runes", 20000, "distill output file rune cap", ""},
}

// ThresholdMetas — the registry's deterministic copy (56 entries — the key set
// identical to the existing ThresholdsKeys, asserted by the guard tests).
func ThresholdMetas() []ThresholdMeta {
	return append([]ThresholdMeta(nil), thresholdMetas...)
}

// ThresholdAutoGrade — the auto-eligibility grade of a registry key ("" = the
// key is manual-only; "shadow" = the v1.23.0 shadow-circuit whitelist). The
// single source is the registry Auto field — the auto surfaces (the what-if
// key gate, the shadow trigger table) admit only non-"" keys.
func ThresholdAutoGrade(key string) string {
	for _, m := range thresholdMetas {
		if m.Key == key {
			return m.Auto
		}
	}
	return ""
}

// ThresholdAutoWhitelist — {key: grade} for every entry with a non-"" Auto
// grade (the initial whitelist is {suggest_min_score: shadow} — guarded for
// exact equality by threshold_meta_test.go).
func ThresholdAutoWhitelist() map[string]string {
	out := map[string]string{}
	for _, m := range thresholdMetas {
		if m.Auto != "" {
			out[m.Key] = m.Auto
		}
	}
	return out
}

// ThresholdMetasAppendForTest — a temporary-registration seam exclusively for the guard
// tests (TestNewKeyPropagates: registering 1 virtual key -> demonstrating simultaneous
// reflection in the 4 derived paths). The returned restore recovers the original registry. Never use on production paths.
func ThresholdMetasAppendForTest(m ThresholdMeta) (restore func()) {
	saved := thresholdMetas
	next := append([]ThresholdMeta(nil), saved...)
	thresholdMetas = append(next, m)
	return func() { thresholdMetas = saved }

}
