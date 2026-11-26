package store

import (
	"fmt"
	"log"
	"math"
	"reflect"
	"strconv"
	"strings"
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

	DroppedDBsWarn int
	StrayDBsWarn   int

	UpdateCacheAgeH int

	CollectionFreshnessH   int
	PluginVersionGapMinors int

	StateStaleDays   int
	StateSnapshotMax int
}

type ThresholdMeta struct {
	Field    string
	Key      string
	Default  any
	Type     string
	Fallback bool
	Seed     bool
	Desc     string
	Auto     string
}

var thresholdMetas = []ThresholdMeta{
	{"PendingMax", "pending_max", 50, "int", false, true, "maintain --check", ""},
	{"DupSuspMax", "dup_susp_max", 20, "int", false, true, "maintain --check", ""},
	{"ConfFloor", "conf_floor", 0.8, "ratio", false, true, "write verdict", ""},
	{"StaleSessions", "stale_sessions", 3, "int", false, true, "done", ""},
	{"StaleDays", "stale_days", 90, "int", true, false, "maintain --stale·report", ""},
	{"StallHotH", "stall_hot_h", 48, "int", true, false, "doctor·maintain", ""},
	{"StallWriteH", "stall_write_h", 72, "int", true, false, "doctor·maintain", ""},
	{"SuggestMinScore", "suggest_min_score", 0.15, "ratio", true, false, "read suggestions", "shadow"},
	{"SuggestScanCap", "suggest_scan_cap", 5000, "int", true, false, "read suggestions", ""},
	{"ConflictDupThreshold", "conflict_dup_threshold", 0.6, "ratio", true, false, "maintain classification", ""},
	{"BackupAgeWarnH", "backup_age_warn_h", 72, "int", true, false, "doctor backup-age", ""},
	{"ZeroNearMinRead", "zero_near_min_read", 10, "int", true, false, "doctor zero-near", ""},
	{"ZeroNearWarnPct", "zero_near_warn_pct", 30, "int", true, false, "doctor zero-near", ""},
	{"PendingAgeDays", "pending_age_days", 7, "int", true, false, "doctor pending-age", ""},
	{"PendingPromoteDays", "pending_promote_days", 3, "int", true, false, "pending promote suggestion (recent N days)", ""},
	{"PendingPromoteMinRunes", "pending_promote_min_runes", 40, "int", true, false, "pending promote suggestion (min body runes)", ""},
	{"InvalidatedPruneDays", "invalidated_prune_days", 90, "int", true, false, "invalidated prune age (maintain --prune-invalidated)", ""},
	{"RecallWarnWrites", "recall_warn_writes", 3, "int-neg", true, false, "done recall warning", ""},
	{"DeadDays", "dead_days", 30, "int", true, false, "report effect", ""},
	{"PairWindowMin", "pair_window_minutes", 30, "int", true, false, "report effect", ""},
	{"ChainWindowH", "chain_window_hours", 24, "int", true, false, "report effect", ""},
	{"EventsRetentionD", "events_retention_days", 14, "int", true, false, "events retention", ""},
	{"HotRetentionD", "hot_events_retention_days", 30, "int", true, false, "events retention", ""},
	{"EventsVolumeWarn", "events_volume_warn", 50000, "int", true, false, "doctor event-volume", ""},
	{"DrillMaxDays", "drill_max_days", 30, "int", true, false, "maintain --check", ""},
	{"DailyRetentionD", "daily_retention_days", 14, "int", true, false, "retention · report/status window", ""},
	{"RrfK", "rrf_k", 60.0, "pos-float", false, false, "read fallback re-ranking", ""},
	{"SecretsScanRows", "secrets_scan_row_limit", 10000, "int", true, false, "doctor secrets row limit", ""},
	{"SessionKnowledgeDays", "session_knowledge_days", 1, "int-neg", true, false, "session knowledge minimum interval", ""},

	{"DiagHotBudgetHigh", "diag_hot_budget_high", 0.8, "ratio", false, false, "report diagnostics", ""},
	{"DiagHotBudgetLow", "diag_hot_budget_low", 0.2, "ratio", false, false, "report diagnostics", ""},
	{"DiagTrendPct", "diag_trend_pct", 50, "int", false, false, "report diagnostics", ""},
	{"DiagRecallLedGoal", "diag_recall_led_goal", 50, "int", false, false, "report diagnostics", ""},
	{"DiagDeadWarn", "diag_dead_warn", 10, "int", false, false, "report diagnostics", ""},
	{"KreadsRetentionD", "knowledge_reads_retention_days", 14, "int", false, false, "db retention", ""},

	{"ExportImportMaxRows", "export_import_max_rows", 100000, "int", false, false, "import row limit", ""},

	{"DecayRate", "decay_rate", 1.0, "pos-float", false, false, "maintain --decay-audit", ""},
	{"ArchiveBelow", "archive_below", 0.0, "nonneg-float", false, false, "maintain --decay-audit", ""},

	{"RecallEventRetentionD", "recall_event_retention_days", 90, "int", true, false, "maintain --prune-invalidated (recall events retention)", ""},

	{"CorrectionConfidence", "correction_confidence", 0.7, "ratio", true, false, "write correction source default", ""},

	{"CorrectionDailyMax", "correction_daily_max", 20, "int", true, false, "write correction daily cap", ""},

	{"GapQueryThreshold", "gap_query_threshold", 3, "int", false, false, "gap candidate accrual threshold", ""},
	{"GapDailyMax", "gap_daily_max", 10, "int", false, false, "gap candidate daily cap", ""},

	{"StaleScoreThreshold", "stale_score_threshold", 70, "int", false, false, "maintain --age-audit over-threshold marking", ""},
	{"StaleTTLDays", "stale_ttl_days", 180, "int", false, false, "maintain --age-audit score TTL saturation", ""},

	{"SurvivedDecayRelief", "survived_decay_relief", 0.0, "nonneg-float", false, false, "survived decay relief (hot·brief·decay-audit)", ""},

	{"EchoTopK", "echo_top_k", 5, "int", false, false, "write echo top-k cap", ""},
	{"EchoMinScore", "echo_min_score", 0.3, "ratio", false, false, "write echo admission floor", ""},
	{"EchoTopicMatch", "echo_topic_match", 0.4, "ratio", false, false, "write echo topic-overlap floor", ""},

	{"WeeklyRollupsRetentionD", "weekly_rollups_retention_days", 90, "int", false, false, "rollup retention", ""},
	{"ThresholdSnapshotsRetentionD", "threshold_snapshots_retention_days", 90, "int", false, false, "snapshot retention", ""},
	{"MeteringFreshnessWarnH", "metering_freshness_warn_h", 336, "int", false, false, "doctor metering-freshness", ""},

	{"VaultScanMaxFiles", "vault_scan_max_files", 10000, "int", false, false, "index vault scan file cap", ""},
	{"VaultSummaryMaxRunes", "vault_summary_max_runes", 200, "int", false, false, "index vault summary truncation runes", ""},
	{"VaultSkipDirs", "vault_skip_dirs", ".obsidian,.trash", "string", false, false, "index vault skip dirs (comma list)", ""},

	{"DistillMinScore", "distill_min_score", 1.0, "nonneg-float", false, false, "distill output minimum recall count", ""},
	{"DistillMaxRunes", "distill_max_runes", 20000, "int", false, false, "distill output file rune cap", ""},

	{"RecallAuditMinBad", "recall_audit_min_bad", 2, "int", false, false, "maintain --recall-audit listing floor", ""},

	{"DoltLogDepthWarn", "dolt_" + "log_depth_warn", 10000, "int", true, false, "doctor depth warning", ""},
	{"DbSizeWarnMB", "db_size_warn_mb", 2048, "int", true, false, "doctor db_size", ""},

	{"DroppedDBsWarn", "dropped_dbs_warn", 1, "int", true, false, "doctor dropped_dbs", ""},
	{"StrayDBsWarn", "stray_dbs_warn", 0, "int", true, false, "doctor stray_dbs", ""},

	{"UpdateCacheAgeH", "update_cache_age_h", 72, "int", true, false, "doctor versions cache age", ""},

	{"CollectionFreshnessH", "collection_freshness_hours", 72, "int", true, false, "doctor collection-freshness (session_starts recency)", ""},
	{"PluginVersionGapMinors", "plugin_version_gap_minors", 2, "int", true, false, "doctor plugin-version-gap", ""},

	{"StateStaleDays", "state_stale_days", 14, "days", false, false, "maintain --stale-state", ""},
	{"StateSnapshotMax", "state_snapshot_max", 3, "count", false, false, "hot", ""},
}

var thresholdIntRanges = map[string][2]int{
	"collection_freshness_hours": {6, 168},
	"plugin_version_gap_minors":  {1, 10},
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

func ThresholdMetaByKey(key string) (ThresholdMeta, bool) {
	for _, m := range thresholdMetas {
		if m.Key == key {
			return m, true
		}
	}
	return ThresholdMeta{}, false
}

func ClampThresholdInt(key string, n int) int {
	r, ok := thresholdIntRanges[key]
	if !ok {
		return n
	}
	if n < r[0] || n > r[1] {
		def := 0
		if m, ok := ThresholdMetaByKey(key); ok {
			def = metaDefaultInt(m.Default)
		}
		log.Printf("[flint] thresholds: %s=%d outside %d..%d — default %d", key, n, r[0], r[1], def)
		return def
	}
	return n
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

func ThresholdMetasReplaceForTest(m ThresholdMeta) (restore func()) {
	saved := thresholdMetas
	next := append([]ThresholdMeta(nil), saved...)
	for i := range next {
		if next[i].Key == m.Key {
			next[i] = m
			thresholdMetas = next
			return func() { thresholdMetas = saved }
		}
	}
	thresholdMetas = append(next, m)
	return func() { thresholdMetas = saved }
}

func DefaultThresholds() Thresholds {
	var th Thresholds
	v := reflect.ValueOf(&th).Elem()
	seen := map[string]bool{}
	for _, m := range thresholdMetas {
		if seen[m.Field] {
			continue
		}
		seen[m.Field] = true
		setThresholdDefault(v.FieldByName(m.Field), m.Default)
	}
	return th
}

func setThresholdDefault(f reflect.Value, d any) {
	if !f.IsValid() || !f.CanSet() {
		return
	}
	switch x := d.(type) {
	case int:
		if f.Kind() == reflect.Int {
			f.SetInt(int64(x))
		}
	case float64:
		if f.Kind() == reflect.Float64 {
			f.SetFloat(x)
		}
	case string:
		if f.Kind() == reflect.String {
			f.SetString(x)
		}
	}
}

func metaDefaultInt(d any) int {
	n, _ := d.(int)
	return n
}

func metaDefaultFloat(d any) float64 {
	x, _ := d.(float64)
	return x
}

func metaDefaultString(d any) string {
	s, _ := d.(string)
	return s
}

func ParseThresholds(rows [][2]any) (Thresholds, error) {
	th := Thresholds{}
	v := reflect.ValueOf(&th).Elem()
	for _, m := range thresholdMetas {
		if m.Seed {
			setThresholdDefault(v.FieldByName(m.Field), m.Default)
		}
	}
	for _, row := range rows {
		key, val := row[0], row[1]
		ks, ok := key.(string)
		if !ok {
			return th, fmt.Errorf("thresholds: key is not a string: %v", key)
		}
		for _, m := range thresholdMetas {
			if m.Key != ks {
				continue
			}
			applyThresholdValue(v.FieldByName(m.Field), m, val, ks)
			break
		}
	}
	return th, nil
}

func applyThresholdValue(f reflect.Value, m ThresholdMeta, val any, ks string) {
	if !f.IsValid() || !f.CanSet() {
		return
	}
	switch m.Type {
	case "int", "days", "count":
		if f.Kind() != reflect.Int {
			return
		}
		f.SetInt(int64(parseIntOrDefault(val, metaDefaultInt(m.Default), ks)))
	case "int-neg":
		if f.Kind() != reflect.Int {
			return
		}
		f.SetInt(int64(parseIntInRangeOrDefault(val, metaDefaultInt(m.Default), -1, ks)))
	case "ratio":
		if f.Kind() != reflect.Float64 {
			return
		}
		f.SetFloat(parseFloatOrDefault(val, metaDefaultFloat(m.Default), ks))
	case "pos-float":
		if f.Kind() != reflect.Float64 {
			return
		}
		f.SetFloat(parseFloatPositive(val, metaDefaultFloat(m.Default), ks))
	case "nonneg-float":
		if f.Kind() != reflect.Float64 {
			return
		}
		f.SetFloat(parseFloatNonNegative(val, metaDefaultFloat(m.Default), ks))
	case "string":
		if f.Kind() != reflect.String {
			return
		}
		f.SetString(parseStringOrDefault(val, metaDefaultString(m.Default), ks))
	}
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

var tierCapKeys = []string{"manual", "hook", "import"}

var tierCapDefaults = map[string]float64{
	"manual": 1.0,
	"hook":   0.9,
	"import": 0.8,
}

const tierCapKeyPrefix = "tier_cap_"

func TierCapKeys() []string {
	return append([]string(nil), tierCapKeys...)
}

func DefaultTierCaps() map[string]float64 {
	out := make(map[string]float64, len(tierCapKeys))
	for _, k := range tierCapKeys {
		out[k] = tierCapDefaults[k]
	}
	return out
}

func ParseTierCaps(rows [][2]any) map[string]float64 {
	out := DefaultTierCaps()
	registered := map[string]bool{}
	for _, k := range tierCapKeys {
		registered[k] = true
	}
	for _, row := range rows {
		key, ok := row[0].(string)
		if !ok || !strings.HasPrefix(key, tierCapKeyPrefix) {
			continue
		}
		channel := strings.TrimPrefix(key, tierCapKeyPrefix)
		if !registered[channel] {
			continue
		}
		out[channel] = parseFloatOrDefault(row[1], tierCapDefaults[channel], key)
	}
	return out
}

func TierCapFor(caps map[string]float64, channel string) (float64, bool) {
	v, ok := caps[channel]
	return v, ok
}

func TierChannel(origin, sourcePath string) string {
	o := strings.ToLower(strings.TrimSpace(origin))
	switch {
	case strings.HasPrefix(o, "import:"):
		return "import"
	case o == "hook":
		return "hook"
	case strings.TrimSpace(sourcePath) != "":
		return "import"
	default:
		return "manual"
	}
}

func CheckTierCap(caps map[string]float64, channel string, confidence float64) error {
	cap, ok := caps[channel]
	if !ok || confidence <= cap {
		return nil
	}
	return &SchemaError{Field: "confidence",
		Expect: fmt.Sprintf("tier cap %s <= %v — lower only", channel, cap)}
}
