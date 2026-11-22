package store

import (
	"fmt"
	"log"
	"math"
	"strconv"
)

// Thresholds are the threshold constants read from the meta table (global constraint: no hardcoding).
type Thresholds struct {
	PendingMax    int
	DupSuspMax    int
	ConfFloor     float64
	StaleSessions int
	// the stale-scan and stall-diagnostic thresholds.
	StaleDays   int // the maintain --stale reference days (the Confluence convention, 90)
	StallHotH   int // the last_hot_at stall grace hours (following Healthchecks.io, 48)
	StallWriteH int // the last_write_at stall grace hours (72)
	// the tuning-constant registry (personal tuning values exist only as
	// meta overrides — the no-commit-to-code-or-docs principle).
	SuggestMinScore        float64 // the minimum adoption similarity for suggestions (did-you-mean)
	SuggestScanCap         int     // the suggestion scan row cap (an unbounded-scan defense)
	ConflictDupThreshold   float64 // the similarity criterion classifying contradiction vs duplicate (at or above = duplicate)
	BackupAgeWarnH         int     // the backup-freshness warning hours (doctor backup-age)
	ZeroNearMinRead        int     // the zero_near sample floor (doctor)
	ZeroNearWarnPct        int     // the zero_near warning ratio % (doctor)
	PendingAgeDays         int     // the pending expiry-guidance days (doctor)
	PendingPromoteDays     int     // the pending promotion-recommendation approximation: the last N days
	PendingPromoteMinRunes int     // the minimum rune count of a promotion-recommendation body
	InvalidatedPruneDays   int     // the invalidated cleanup days
	// the done recall warning — the write-count threshold for a 0-recalls-today state.
	RecallWarnWrites int // at or above, the "writes only, no recalls" warning (done)
	// the effect-metering decision windows and retention.
	DeadDays             int     // the dead_pct reference days (default 30)
	PairWindowMin        int     // the read->write pair-decision window (minutes, default 30)
	ChainWindowH         int     // the usage-chain decision window (hours, default 24)
	EventsRetentionD     int     // read_events and session_starts retention (days, default 14)
	HotRetentionD        int     // hot_events retention (days, default 30 — aligned with the dead guard window)
	EventsVolumeWarn     int     // the doctor metering-table warning row count (default 50000)
	DrillMaxDays         int     // the drill elapsed-report days (default 30)
	DailyRetentionD      int     // the daily:* retention / windowed-metric window cap (default 14)
	RrfK                 float64 // the search RRF rank-softening coefficient (default 60)
	SecretsScanRows      int     // the doctor secrets per-scan row cap (default 10000)
	SessionKnowledgeDays int     // the session-knowledge minimum interval (days, default 1 — -1 = off)
	// the rule-diagnostics D thresholds (spec §3 diag_*, TUNING §2). Tier B
	// inheritance-reads doctor's existing keys (zero_near_warn_pct,
	// pending_age_days) (preventing duplicate keys — new keys exist only for
	DiagHotBudgetHigh float64 // tier A/C self-reference and value defaults).
	DiagHotBudgetLow  float64 // the hot injection mean/budget 'chronic truncation' verdict upper bound (default 0.8)
	DiagTrendPct      int     // the hot injection mean/budget informativeness (headroom) verdict lower bound (default 0.2)
	DiagRecallLedGoal int     // the hot bytes sharp-trend verdict % (default 50)
	DiagDeadWarn      int     // the recall-lead sessions' 'default goal' % (a tier-C value default, default 50)
	KreadsRetentionD  int     // the dead_pct diagnostic warning % (default 10)
	// the knowledge_reads retention (days, default 14)
	// the export/import shared per-run row cap. A memory defense against
	ExportImportMaxRows int // parsing and accruing large document sets in full (mitigating the
	// the decay-score decay and archive-recommendation thresholds (consuming the
	// schema v11 columns recall_score and last_recalled_at — ops maintain --decay-audit).
	DecayRate    float64 // the recall decay coefficient (points/day, default 1)
	ArchiveBelow float64 // below this after decay, archive is recommended (default 0 = inactive)
	// the recall_events retention (the growth cap of the schema v12 append-only
	// recall source — maintain --prune-invalidated cleans expired events).
	RecallEventRetentionD int // the recall-event retention (days, default 90)
	// the --source correction write default (v1.11.0): the confidence applied
	// when a correction payload omits it (write.go applyCorrectionDefaults).
	CorrectionConfidence float64 // default 0.7 — below conf_floor so a correction lands pending
	// the --source correction daily cap (v1.11.0): correction lines beyond
	// this many committed accruals per UTC day are quietly skipped with a
	// warning (write.go — the daily:correction_write flat-key count).
	CorrectionDailyMax int // default 20
	// the gap-candidate accrual trigger and daily cap (v1.12.0): the daily
	// gapq counter reaching the threshold accrues a gap candidate; the cap
	// bounds new ADDs per UTC day (knowledge/write.go gap source).
	GapQueryThreshold int // default 3 — same-normalized-query recall-failure count triggering a candidate
	GapDailyMax       int // default 10 — only new ADDs consume the cap (updates don't, the correction convention)
	// the stale-score report thresholds (v1.13.0 — ops maintain --age-audit:
	// the over-threshold marking line and the StaleScore TTL saturation days).
	StaleScoreThreshold int // default 70 — at or above, an age-audit item is marked over_threshold
	StaleTTLDays        int // default 180 — the TTL saturation of the stale score formula
	// the survived decay relief (v1.14.0 — the verified-success weighting):
	// points of decay offset per survived session in the effective-score
	// formula count − max(0, rate×days − survived×relief). Default 0 =
	// inactive (the release-time invariance ruling — relief is opt-in only).
	SurvivedDecayRelief float64 // default 0
	// the echo-scan keys (v1.17.0 — knowledge write echo response): the top-k
	// candidate cap, the admission score floor, and the topic-overlap floor
	// (knowledge/echo.go EchoScan — the correction convention).
	EchoTopK       int     // default 5 — the max echo candidates in a write response
	EchoMinScore   float64 // default 0.3 — the echo candidate admission floor (above suggest 0.15 — write-path scan cost)
	EchoTopicMatch float64 // default 0.4 — the topic-overlap floor of an echo candidate
	// the schema v13 observability keys (v1.19.0): the weekly_rollups·
	// threshold_snapshots retention and the metering-freshness warning
	// hours (doctor metering-freshness).
	WeeklyRollupsRetentionD      int // default 90 — the weekly_rollups retention (days, the *_retention_days convention)
	ThresholdSnapshotsRetentionD int // default 90 — the threshold_snapshots retention (days)
	MeteringFreshnessWarnH       int // default 336 — the metering-freshness warning hours (14 days, aligned with the events retention default)
	// the vault-bridge keys (v1.21.0 — the schema v16 vault_files index):
	// the per-scan .md file cap, the summary truncation runes, and the
	// comma-separated skip-dir list (the first string-valued key family —
	// unset keeps "" and the consumer falls back to DefaultThresholds).
	VaultScanMaxFiles    int    // default 10000 — the per-scan vault .md file cap
	VaultSummaryMaxRunes int    // default 200 — the summary truncation (TruncateRunes)
	VaultSkipDirs        string // default ".obsidian,.trash" — comma-separated skip dirs
	// the distill output floor (v1.22.0 — cli distill): the minimum
	// raw recall count of an emitted insight row (recall_events derived
	// count-scale, so values above 1 are normal — the archive_below
	// parser convention).
	DistillMinScore float64 // default 1.0 — at least one raw recall
	// the distill output file cap (v1.22.1 r97 — cli distill): the
	// per-run insights file rune ceiling. Rows past the cap stay
	// unstamped and defer to the next run (ADR-0018).
	DistillMaxRunes int // default 20000
}

// Defaults — pending > 50, duplicate-suspects > 20, conf < 0.8, 3 consecutive sessions, stale 90 days, stall 48/72h.
const (
	defaultPendingMax    = 50
	defaultDupSuspMax    = 20
	defaultConfFloor     = 0.8
	defaultStaleSessions = 3
	defaultStaleDays     = 90
	defaultStallHotH     = 48
	defaultStallWriteH   = 72
	// the defaults of the registry tuning constants (a single source).
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
	// the effect-metering decision-window and retention defaults.
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
	// the diagnostic diag_* and knowledge_reads retention defaults (spec §3, §1.2).
	defaultDiagHotBudgetHigh   = 0.8
	defaultDiagHotBudgetLow    = 0.2
	defaultDiagTrendPct        = 50
	defaultDiagRecallLedGoal   = 50
	defaultDiagDeadWarn        = 10
	defaultKreadsRetentionD    = 14
	defaultExportImportMaxRows = 100000
	// the decay and archive-recommendation defaults.
	defaultDecayRate    = 1.0
	defaultArchiveBelow = 0.0
	// the recall_events retention default (90 days — in the EventEffectiveScore
	// formula count - rate x days_since(last), with the default rate 1.0, only rows
	// whose elapsed days are at least count vanish to the effective-score floor of
	// 0. High-frequency rows keep a positive score, so cleanup can shift the
	// ranking. For rows with recent recalls, only events inside the retention
	// window count — the intended growth boundary via the window-cap effect).
	defaultRecallEventRetentionD = 90
	// the --source correction write default confidence (v1.11.0 — pending at
	// review, below the 0.8 conf_floor).
	defaultCorrectionConfidence = 0.7
	// the --source correction daily accrual cap (v1.11.0).
	defaultCorrectionDailyMax = 20
	// the gap-candidate accrual trigger and daily cap (v1.12.0).
	defaultGapQueryThreshold = 3
	defaultGapDailyMax       = 10
	// the stale-score report thresholds (v1.13.0 — the consumer fallback basis,
	// the correction 2-key convention).
	defaultStaleScoreThreshold = 70
	defaultStaleTTLDays        = 180
	// the survived decay relief default (0 = inactive — the opt-in ruling).
	defaultSurvivedDecayRelief = 0.0
	// the echo-scan defaults (v1.17.0): min_score 0.3 sits above
	// suggest_min_score 0.15 — the write path pays a per-write scan cost, so
	// the admission bar is raised (scan-cost control).
	defaultEchoTopK       = 5
	defaultEchoMinScore   = 0.3
	defaultEchoTopicMatch = 0.4
	// the schema v13 observability defaults (v1.19.0): the retention pair
	// follows the *_retention_days convention (90 — the recall_event family);
	// the freshness window is 14 days in hours, aligned with the events
	// retention default (a lag past the retention horizon is the warning
	// target).
	defaultWeeklyRollupsRetentionD      = 90
	defaultThresholdSnapshotsRetentionD = 90
	defaultMeteringFreshnessWarnH       = 336
	// the vault-bridge defaults (v1.21.0): the skip dirs follow the
	// Obsidian default hidden dir + the trash folder (synthetic defaults —
	// personal vault layouts live only in meta overrides).
	defaultVaultScanMaxFiles    = 10000
	defaultVaultSummaryMaxRunes = 200
	defaultVaultSkipDirs        = ".obsidian,.trash"
	// the distill output floor default (v1.22.0): a raw recall count of
	// at least 1 (one surviving recall — the count-scale basis).
	defaultDistillMinScore = 1.0
	// the distill output file cap default (v1.22.1 r97): 20000 runes per
	// insights file — the remainder defers to the next run.
	defaultDistillMaxRunes = 20000
)

// DefaultThresholds is the all-keys defaults struct.
func DefaultThresholds() Thresholds {
	return Thresholds{
		PendingMax:    defaultPendingMax,
		DupSuspMax:    defaultDupSuspMax,
		ConfFloor:     defaultConfFloor,
		StaleSessions: defaultStaleSessions,
		StaleDays:     defaultStaleDays,
		StallHotH:     defaultStallHotH,
		StallWriteH:   defaultStallWriteH,
		// the v0.16.3 keys — unlike ParseThresholds, which keeps 0 when unset,
		// these are always the defaults (the consumer fallback basis).
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
		// the diagnostic keys — the consumer (ops.Diagnose) fallback basis values.
		DiagHotBudgetHigh: defaultDiagHotBudgetHigh,
		DiagHotBudgetLow:  defaultDiagHotBudgetLow,
		DiagTrendPct:      defaultDiagTrendPct,
		DiagRecallLedGoal: defaultDiagRecallLedGoal,
		DiagDeadWarn:      defaultDiagDeadWarn,
		KreadsRetentionD:  defaultKreadsRetentionD,
		// DefaultThresholds is always the defaults (separate from the Parse
		// unset-0 convention — the consumer fallback basis).
		ExportImportMaxRows: defaultExportImportMaxRows,
		// the decay and archive-recommendation values (the consumer fallback basis).
		DecayRate:    defaultDecayRate,
		ArchiveBelow: defaultArchiveBelow,
		// the recall_events retention (the consumer fallback basis).
		RecallEventRetentionD: defaultRecallEventRetentionD,
		// the correction write default (the consumer fallback basis).
		CorrectionConfidence: defaultCorrectionConfidence,
		CorrectionDailyMax:   defaultCorrectionDailyMax,
		// the gap-candidate trigger and cap (the consumer fallback basis).
		GapQueryThreshold: defaultGapQueryThreshold,
		GapDailyMax:       defaultGapDailyMax,
		// the stale-score report thresholds (the consumer fallback basis).
		StaleScoreThreshold: defaultStaleScoreThreshold,
		StaleTTLDays:        defaultStaleTTLDays,
		// the survived decay relief (the consumer fallback basis — 0 = inactive).
		SurvivedDecayRelief: defaultSurvivedDecayRelief,
		// the echo-scan keys (the consumer fallback basis).
		EchoTopK:       defaultEchoTopK,
		EchoMinScore:   defaultEchoMinScore,
		EchoTopicMatch: defaultEchoTopicMatch,
		// the schema v13 observability keys (the consumer fallback basis).
		WeeklyRollupsRetentionD:      defaultWeeklyRollupsRetentionD,
		ThresholdSnapshotsRetentionD: defaultThresholdSnapshotsRetentionD,
		MeteringFreshnessWarnH:       defaultMeteringFreshnessWarnH,
		// the vault-bridge keys (the consumer fallback basis — the string
		// key's unset sentinel is "" and falls back here too).
		VaultScanMaxFiles:    defaultVaultScanMaxFiles,
		VaultSummaryMaxRunes: defaultVaultSummaryMaxRunes,
		VaultSkipDirs:        defaultVaultSkipDirs,
		// the distill output floor (the consumer fallback basis).
		DistillMinScore: defaultDistillMinScore,
		// the distill output file cap (the consumer fallback basis).
		DistillMaxRunes: defaultDistillMaxRunes,
	}
}

// ParseThresholds converts meta-table key-value read results (rows) into Thresholds.
// v0.5 keys fall back to defaults when unset; invalid values give defaults + a
// warning (not an error). The v0.6 keys (stale_days, stall_hot_h, stall_write_h) and the v0.16.3 keys
// (suggest_min_score·suggest_scan_cap·conflict_dup_threshold·
// backup_age_warn_h, zero_near_min_read, zero_near_warn_pct, pending_age_days
// keep 0 (unset) when unset — the consumer (loadThresholds) falls back to the
// DefaultThresholds values. (A convention for extending fields without
// regressing the existing whole-struct comparison tests. Invalid values get
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
			// accepts min=-1 (-1 = off). 0 is indistinguishable from unset —
			// loadThresholds falls back to the default (documented in TUNING §2).
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
			// rrf_k under a 0..1 clamping parser would reject the default 60
			// entirely, making an override fundamentally impossible (demonstrated). A
			// dedicated parser allows the full positive range — RRF k is typically tens to hundreds (see the spec).
			th.RrfK = parseFloatPositive(val, defaultRrfK, ks)
		case "secrets_scan_row_limit":
			th.SecretsScanRows = parseIntOrDefault(val, defaultSecretsScanRows, ks)
		case "session_knowledge_days":
			// the -1 = off sentinel (inheriting the recall_warn_writes convention).
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
			// unset keeps 0 (the convention: extend without regressing the
			// whole-struct comparison tests. The consumer falls back to DefaultThresholds).
			th.ExportImportMaxRows = parseIntOrDefault(val, defaultExportImportMaxRows, ks)
		case "decay_rate":
			// unset keeps 0 (the consumer, ops, falls back to the default 1).
			// values <= 0 fall back (a decay-off tuning is, unlike archive_below=0, not provided).
			th.DecayRate = parseFloatPositive(val, defaultDecayRate, ks)
		case "archive_below":
			// 0 = inactive is a normal value, so only negatives are rejected (score
			// thresholds above 1 are also normal — the parseFloatOrDefault 0..1 clamp does not fit).
			th.ArchiveBelow = parseFloatNonNegative(val, defaultArchiveBelow, ks)
		case "recall_event_retention_days":
			// unset keeps 0 (the convention — the consumer, maintain --prune-invalidated,
			// falls back to the DefaultThresholds 90 — the same family as the kreads retention key).
			th.RecallEventRetentionD = parseIntOrDefault(val, defaultRecallEventRetentionD, ks)
		case "correction_confidence":
			// unset keeps 0 (the convention — the consumer, write's correction
			// default, falls back to DefaultThresholds via ApplyThresholdsFallback).
			th.CorrectionConfidence = parseFloatOrDefault(val, defaultCorrectionConfidence, ks)
		case "correction_daily_max":
			// unset keeps 0 (the convention — the consumer, write's correction
			// daily cap, falls back via ApplyThresholdsFallback).
			th.CorrectionDailyMax = parseIntOrDefault(val, defaultCorrectionDailyMax, ks)
		case "gap_query_threshold":
			// unset keeps 0 (the convention — the consumer, write's gap
			// accrual, falls back via ApplyThresholdsFallback).
			th.GapQueryThreshold = parseIntOrDefault(val, defaultGapQueryThreshold, ks)
		case "gap_daily_max":
			// unset keeps 0 (the convention — the consumer, write's gap
			// daily cap, falls back via ApplyThresholdsFallback).
			th.GapDailyMax = parseIntOrDefault(val, defaultGapDailyMax, ks)
		case "stale_score_threshold":
			// unset keeps 0 (the convention — the consumer, ops --age-audit
			// marking, falls back via the DefaultThresholds basis).
			th.StaleScoreThreshold = parseIntOrDefault(val, defaultStaleScoreThreshold, ks)
		case "stale_ttl_days":
			// unset keeps 0 (the convention — the consumer, ops StaleScore's
			// TTL saturation, falls back via the DefaultThresholds basis).
			th.StaleTTLDays = parseIntOrDefault(val, defaultStaleTTLDays, ks)
		case "survived_decay_relief":
			// 0 = inactive is a normal value (the v1.14.0 opt-in ruling), so
			// only negatives are rejected (the archive_below convention).
			th.SurvivedDecayRelief = parseFloatNonNegative(val, defaultSurvivedDecayRelief, ks)
		case "echo_top_k":
			// unset keeps 0 (the convention — the consumer, write's echo scan,
			// falls back via the DefaultThresholds basis).
			th.EchoTopK = parseIntOrDefault(val, defaultEchoTopK, ks)
		case "echo_min_score":
			// unset keeps 0 (the convention — the 0..1 clamp parser, the
			// suggest_min_score family).
			th.EchoMinScore = parseFloatOrDefault(val, defaultEchoMinScore, ks)
		case "echo_topic_match":
			// unset keeps 0 (the convention — the 0..1 clamp parser).
			th.EchoTopicMatch = parseFloatOrDefault(val, defaultEchoTopicMatch, ks)
		case "weekly_rollups_retention_days":
			// unset keeps 0 (the convention — the consumer, the rollup
			// retention prune, falls back via the DefaultThresholds basis).
			th.WeeklyRollupsRetentionD = parseIntOrDefault(val, defaultWeeklyRollupsRetentionD, ks)
		case "threshold_snapshots_retention_days":
			// unset keeps 0 (the convention — the consumer, the snapshot
			// retention prune, falls back via the DefaultThresholds basis).
			th.ThresholdSnapshotsRetentionD = parseIntOrDefault(val, defaultThresholdSnapshotsRetentionD, ks)
		case "metering_freshness_warn_h":
			// unset keeps 0 (the convention — the consumer, doctor
			// metering-freshness, falls back via the DefaultThresholds basis).
			th.MeteringFreshnessWarnH = parseIntOrDefault(val, defaultMeteringFreshnessWarnH, ks)
		case "vault_scan_max_files":
			// unset keeps 0 (the convention — the consumer, the vault scan,
			// falls back via the DefaultThresholds basis).
			th.VaultScanMaxFiles = parseIntOrDefault(val, defaultVaultScanMaxFiles, ks)
		case "vault_summary_max_runes":
			// unset keeps 0 (the convention — the consumer, the vault summary
			// truncation, falls back via the DefaultThresholds basis).
			th.VaultSummaryMaxRunes = parseIntOrDefault(val, defaultVaultSummaryMaxRunes, ks)
		case "vault_skip_dirs":
			// unset keeps "" (the string analog of the unset-keeps-0
			// convention — the consumer falls back to DefaultThresholds; any
			// string value, empty included, passes through verbatim).
			th.VaultSkipDirs = parseStringOrDefault(val, defaultVaultSkipDirs, ks)
		case "distill_min_score":
			// unset keeps 0 (the convention — the consumer, cli distill,
			// falls back via the DefaultThresholds basis). A stored 0 is
			// the UNSET sentinel, not "no floor" — the consumer only
			// applies values > 0, so no-floor exists solely through the
			// --min-score 0 flag (v1.22.1 r97-2 correction). The score is
			// a raw recall count (values above 1 normal), so only
			// negatives are rejected (the archive_below convention).
			th.DistillMinScore = parseFloatNonNegative(val, defaultDistillMinScore, ks)
		case "distill_max_runes":
			// unset keeps 0 (the convention — the consumer, cli distill,
			// falls back via the DefaultThresholds basis; 0 cannot be a
			// meaningful cap, so the consumer applies values > 0 only).
			th.DistillMaxRunes = parseIntOrDefault(val, defaultDistillMaxRunes, ks)
		}
	}
	return th, nil
}

// toString normalizes an any value to a string (accepting numeric types).
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

// parseIntOrDefault gives defaults + a warning on integer parse failure.
// parseIntOrDefault — integer parsing (lower bound 0: negatives give the
// default). An alias of the generalized lower-bound body (parseIntInRangeOrDefault).
func parseIntOrDefault(val any, def int, key string) int {
	return parseIntInRangeOrDefault(val, def, 0, key)
}

// parseIntInRangeOrDefault — accepts only >= min (below min or unparsable
// gives the default). Keys using a sentinel (e.g. recall_warn_writes -1 = off) extend with min=-1.
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

// parseFloatOrDefault gives defaults + a warning on float parse failure.
// parseFloatPositive — a dedicated parser allowing positives (> 0). For keys
// like rrf_k (default 60) where values outside 0..1 are normal.
func parseFloatPositive(val any, def float64, key string) float64 {
	s := fmt.Sprint(val)
	f, err := strconv.ParseFloat(s, 64)
	// rejects NaN and Inf — in Go, NaN<=0 is false, so it would pass, and
	// rrf_k=NaN silently rendered the whole fallback score meaningless (demonstrated by a probe).
	if err != nil || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		log.Printf("[flint] thresholds: cannot parse %s=%q (positive required) — default %v", key, s, def)
		return def
	}
	return f
}

// parseStringOrDefault — string parsing (v1.21.0 vault_skip_dirs — the
// first string-valued key family): a toString-unsupported value type
// gives the default + a warning; any string value (empty included — the
// legal "no skip dirs" override) passes through verbatim. The unset
// sentinel is "" and the consumer falls back to DefaultThresholds (the
// unset-keeps-0 convention's string analog).
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
	// rejects NaN (both NaN<0 and NaN>1 are false, so it would pass).
	if err != nil || f < 0 || f > 1 || math.IsNaN(f) {
		log.Printf("[flint] thresholds: cannot parse %s=%q — default %v", key, s, def)
		return def
	}
	return f
}

// parseFloatNonNegative — a parser accepting the full range >= 0. For score-
// threshold keys like archive_below (where the default 0 is both the inactive
// sentinel and a threshold above 1 is normal) where parseFloatOrDefault's 0..1 clamp does not fit. Rejects NaN, Inf, and negatives.
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
