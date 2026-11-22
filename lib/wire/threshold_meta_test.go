// threshold_meta_test.go — the registry guard.
// TestThresholdRegistrySingleSource (the registry key set == the existing key
// set, Parse recognition, tuningKeys, and the TUNING-table exposure — 4-way
// homogeneity) and TestNewKeyPropagates (registering 1 virtual key reflects in
// all 4 paths simultaneously — registered then removed). An external wire test package — imports ops to verify the ops-derived path (TuningKeys).
package wire_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// frozenThresholdKeys — the ThresholdsKeys original (order included). The baseline
// asserting the key set and order stay unchanged after the registry migration.
var frozenThresholdKeys = []string{
	"pending_max", "dup_susp_max", "conf_floor", "stale_sessions",
	"stale_days", "stall_hot_h", "stall_write_h",
	"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
	"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
	"pending_age_days", "pending_promote_days", "pending_promote_min_runes", "invalidated_prune_days", "recall_warn_writes",
	"dead_days", "pair_window_minutes", "chain_window_hours",
	"events_retention_days", "hot_events_retention_days",
	"events_volume_warn", "drill_max_days", "daily_retention_days", "rrf_k", "secrets_scan_row_limit", "session_knowledge_days",
	// the diagnostics engine and kreads retention (diagnosis.go, TUNING §2)
	"diag_hot_budget_high", "diag_hot_budget_low", "diag_trend_pct", "diag_recall_led_goal", "diag_dead_warn", "knowledge_reads_retention_days",
	// the import row cap (knowledge/import_jsonl.go)
	"export_import_max_rows",
	// the decay coefficient and archive-recommendation thresholds (ops --decay-audit)
	"decay_rate", "archive_below",
	// the recall_events retention (maintain --prune-invalidated event cleanup)
	"recall_event_retention_days",
	// the --source correction write default (knowledge/write.go)
	"correction_confidence",
	// the --source correction daily accrual cap (knowledge/write.go)
	"correction_daily_max",
	// the gap-candidate accrual trigger and daily cap (v1.12.0 — knowledge/write.go gap source)
	"gap_query_threshold", "gap_daily_max",
	// the stale-score report marking threshold and the TTL saturation days (v1.13.0 — ops maintain --age-audit)
	"stale_score_threshold", "stale_ttl_days",
	// the survived decay relief (v1.14.0 — hot·brief ranking·maintain --decay-audit)
	"survived_decay_relief",
	// the echo-scan keys (v1.17.0 — knowledge write echo response, the correction convention)
	"echo_top_k", "echo_min_score", "echo_topic_match",
	// the schema v13 observability keys (v1.19.0 — rollup·snapshot retention, metering freshness)
	"weekly_rollups_retention_days", "threshold_snapshots_retention_days", "metering_freshness_warn_h",
	// the vault-bridge keys (v1.21.0 — schema v16 vault_files: scan cap, summary runes, skip dirs)
	"vault_scan_max_files", "vault_summary_max_runes", "vault_skip_dirs",
	// the distill output floor and file cap (v1.22.0·v1.22.1 r97 — schema v17 distill command)
	"distill_min_score", "distill_max_runes",
}

// TestStaleThresholdDefaultsOverride — the v1.13.0 age-audit keys: defaults
// (70 · 180), meta override reflection, and the unset-keeps-0 convention
// (the consumer falls back to DefaultThresholds — the gap 2-key convention).
func TestStaleThresholdDefaultsOverride(t *testing.T) {
	// defaults.
	def := store.DefaultThresholds()
	if def.StaleScoreThreshold != 70 {
		t.Errorf("StaleScoreThreshold 기본값=%d, want 70", def.StaleScoreThreshold)
	}
	if def.StaleTTLDays != 180 {
		t.Errorf("StaleTTLDays 기본값=%d, want 180", def.StaleTTLDays)
	}
	// override (the verify-gate ruling values: threshold 50 · TTL 90).
	th, err := store.ParseThresholds([][2]any{
		{"stale_score_threshold", "50"},
		{"stale_ttl_days", "90"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.StaleScoreThreshold != 50 {
		t.Errorf("stale_score_threshold 오버라이드=%d, want 50", th.StaleScoreThreshold)
	}
	if th.StaleTTLDays != 90 {
		t.Errorf("stale_ttl_days 오버라이드=%d, want 90", th.StaleTTLDays)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.StaleScoreThreshold != 0 || unset.StaleTTLDays != 0 {
		t.Errorf("unset 폴백: stale_score_threshold=%d stale_ttl_days=%d, want 0·0", unset.StaleScoreThreshold, unset.StaleTTLDays)
	}
}

// TestGapThresholdDefaultsOverride — the v1.12.0 gap keys: defaults (3 · 10),
// meta override reflection, and the unset-keeps-0 convention (the consumer
// falls back to DefaultThresholds — the correction 2-key convention).
func TestGapThresholdDefaultsOverride(t *testing.T) {
	// defaults.
	def := store.DefaultThresholds()
	if def.GapQueryThreshold != 3 {
		t.Errorf("GapQueryThreshold 기본값=%d, want 3", def.GapQueryThreshold)
	}
	if def.GapDailyMax != 10 {
		t.Errorf("GapDailyMax 기본값=%d, want 10", def.GapDailyMax)
	}
	// override.
	th, err := store.ParseThresholds([][2]any{
		{"gap_query_threshold", "5"},
		{"gap_daily_max", "2"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.GapQueryThreshold != 5 {
		t.Errorf("gap_query_threshold 오버라이드=%d, want 5", th.GapQueryThreshold)
	}
	if th.GapDailyMax != 2 {
		t.Errorf("gap_daily_max 오버라이드=%d, want 2", th.GapDailyMax)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.GapQueryThreshold != 0 || unset.GapDailyMax != 0 {
		t.Errorf("unset 폴백: gap_query_threshold=%d gap_daily_max=%d, want 0·0", unset.GapQueryThreshold, unset.GapDailyMax)
	}
}

// TestSurvivedReliefThresholdDefaultsOverride — the v1.14.0 T4 survived
// decay-relief key: default 0 (inactive — the release-time invariance
// ruling), meta override reflection, negative rejection, and the
// unset-keeps-0 convention (the consumer falls back to DefaultThresholds).
func TestSurvivedReliefThresholdDefaultsOverride(t *testing.T) {
	// defaults — 0 = inactive (survived relief is opt-in only).
	def := store.DefaultThresholds()
	if def.SurvivedDecayRelief != 0 {
		t.Errorf("SurvivedDecayRelief 기본값=%v, want 0(비활성)", def.SurvivedDecayRelief)
	}
	// override.
	th, err := store.ParseThresholds([][2]any{
		{"survived_decay_relief", "1.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.SurvivedDecayRelief != 1.5 {
		t.Errorf("survived_decay_relief 오버라이드=%v, want 1.5", th.SurvivedDecayRelief)
	}
	// negative rejected — falls back to the default 0 (non-negative parser).
	neg, err := store.ParseThresholds([][2]any{
		{"survived_decay_relief", "-0.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.SurvivedDecayRelief != 0 {
		t.Errorf("음수 거부=%v, want 0(폴백)", neg.SurvivedDecayRelief)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.SurvivedDecayRelief != 0 {
		t.Errorf("unset 폴백: survived_decay_relief=%v, want 0", unset.SurvivedDecayRelief)
	}
}

// TestEchoThresholdDefaultsOverride — the v1.17.0 echo keys: defaults
// (5 · 0.3 · 0.4), meta override reflection, and the unset-keeps-0 convention
// (the consumer falls back to DefaultThresholds — the correction convention).
// echo_min_score 0.3 is above suggest_min_score 0.15: the write-path scan pays
// per-write cost, so the admission bar is raised (scan-cost control).
func TestEchoThresholdDefaultsOverride(t *testing.T) {
	// defaults.
	def := store.DefaultThresholds()
	if def.EchoTopK != 5 {
		t.Errorf("EchoTopK 기본값=%d, want 5", def.EchoTopK)
	}
	if def.EchoMinScore != 0.3 {
		t.Errorf("EchoMinScore 기본값=%v, want 0.3", def.EchoMinScore)
	}
	if def.EchoTopicMatch != 0.4 {
		t.Errorf("EchoTopicMatch 기본값=%v, want 0.4", def.EchoTopicMatch)
	}
	// override.
	th, err := store.ParseThresholds([][2]any{
		{"echo_top_k", "3"},
		{"echo_min_score", "0.5"},
		{"echo_topic_match", "0.6"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.EchoTopK != 3 {
		t.Errorf("echo_top_k 오버라이드=%d, want 3", th.EchoTopK)
	}
	if th.EchoMinScore != 0.5 {
		t.Errorf("echo_min_score 오버라이드=%v, want 0.5", th.EchoMinScore)
	}
	if th.EchoTopicMatch != 0.6 {
		t.Errorf("echo_topic_match 오버라이드=%v, want 0.6", th.EchoTopicMatch)
	}
	// out-of-range rejection — the 0..1 clamp parser (negative falls back).
	neg, err := store.ParseThresholds([][2]any{
		{"echo_min_score", "-0.1"},
		{"echo_topic_match", "1.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.EchoMinScore != 0.3 {
		t.Errorf("echo_min_score 음수 거부=%v, want 0.3(폴백)", neg.EchoMinScore)
	}
	if neg.EchoTopicMatch != 0.4 {
		t.Errorf("echo_topic_match 범위 초과 거부=%v, want 0.4(폴백)", neg.EchoTopicMatch)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.EchoTopK != 0 || unset.EchoMinScore != 0 || unset.EchoTopicMatch != 0 {
		t.Errorf("unset 폴백: echo_top_k=%d echo_min_score=%v echo_topic_match=%v, want 0·0·0",
			unset.EchoTopK, unset.EchoMinScore, unset.EchoTopicMatch)
	}
}

// TestThresholdRegistrySingleSource — the registry is homogeneous with the 4 places
// (the existing key set, Parse recognition, tuningKeys, and the TUNING-table
// exposure) — adding a key to only the registry or only store (the struct, defaults, or the Parse switch) FAILs.
func TestThresholdRegistrySingleSource(t *testing.T) {
	metas := wire.ThresholdMetas()
	if len(metas) != len(frozenThresholdKeys) {
		t.Fatalf("등록표 항목 수: %d, want %d", len(metas), len(frozenThresholdKeys))
	}
	// 1. the existing key set (order included) — since ThresholdsKeys derives from
	// the registry, two-way homogeneity is exactly the derivation contract.
	if got := wire.ThresholdsKeys(); !reflect.DeepEqual(got, frozenThresholdKeys) {
		t.Fatalf("ThresholdsKeys 드리프트:\n got=%v\nwant=%v", got, frozenThresholdKeys)
	}
	seen := map[string]bool{}
	def := store.DefaultThresholds()
	dv := reflect.ValueOf(def)
	for i, m := range metas {
		if m.Key != frozenThresholdKeys[i] {
			t.Errorf("등록표[%d] 키=%q, want %q", i, m.Key, frozenThresholdKeys[i])
		}
		if seen[m.Key] {
			t.Errorf("등록표 중복 키: %q", m.Key)
		}
		seen[m.Key] = true
		fv := dv.FieldByName(m.Field)
		if !fv.IsValid() {
			t.Errorf("%q: Thresholds 필드 무효 %q", m.Key, m.Field)
			continue
		}
		// defaults homogeneity — registry Default == the DefaultThresholds field value.
		// Parse recognition — a distinguishable sentinel value is reflected without a parse fallback.
		var sentinel any
		switch fv.Kind() {
		case reflect.Int:
			if d, ok := m.Default.(int); !ok || int64(d) != fv.Int() {
				t.Errorf("%q: 등록표 기본값 %v != store 기본값 %d", m.Key, m.Default, fv.Int())
			}
			sentinel = "12345"
		case reflect.Float64:
			if d, ok := m.Default.(float64); !ok || d != fv.Float() {
				t.Errorf("%q: 등록표 기본값 %v != store 기본값 %v", m.Key, m.Default, fv.Float())
			}
			sentinel = "0.5"
		case reflect.String:
			// v1.21.0 vault_skip_dirs — the first string-valued key family.
			if d, ok := m.Default.(string); !ok || d != fv.String() {
				t.Errorf("%q: 등록표 기본값 %v != store 기본값 %q", m.Key, m.Default, fv.String())
			}
			sentinel = "zz-sentinel-probe"
		default:
			t.Errorf("%q: 미지원 필드 타입 %v", m.Key, fv.Kind())
			continue
		}
		th, err := store.ParseThresholds([][2]any{{m.Key, sentinel}})
		if err != nil {
			t.Errorf("%q: ParseThresholds err: %v", m.Key, err)
			continue
		}
		pv := reflect.ValueOf(th).FieldByName(m.Field)
		switch pv.Kind() {
		case reflect.Int:
			if pv.Int() != 12345 {
				t.Errorf("%q: Parse 미인식(반영값 %d)", m.Key, pv.Int())
			}
		case reflect.Float64:
			if pv.Float() != 0.5 {
				t.Errorf("%q: Parse 미인식(반영값 %v)", m.Key, pv.Float())
			}
		case reflect.String:
			if pv.String() != "zz-sentinel-probe" {
				t.Errorf("%q: Parse 미인식(반영값 %q)", m.Key, pv.String())
			}
		}
	}
	// 2. tuningKeys homogeneity — TuningKeys derives from the registry (order,
	// Default, Consumer, and Get all from the registry).
	tk := ops.TuningKeys()
	if len(tk) != len(metas) {
		t.Fatalf("TuningKeys 수: %d, want %d(등록표)", len(tk), len(metas))
	}
	for i, info := range tk {
		m := metas[i]
		if info.Key != m.Key {
			t.Errorf("TuningKeys[%d] 키=%q, want %q", i, info.Key, m.Key)
		}
		if info.Consumer != m.Desc {
			t.Errorf("%q: Consumer=%q, want 등록표 Desc %q", m.Key, info.Consumer, m.Desc)
		}
		if info.Get(def) != info.Default {
			t.Errorf("%q: Get(기본값)=%q != Default=%q", m.Key, info.Get(def), info.Default)
		}
		var want string
		switch d := m.Default.(type) {
		case int:
			want = fmtInt(d)
		case float64:
			want = fmtFloat(d)
		case string:
			want = d
		}
		if info.Default != want {
			t.Errorf("%q: Default=%q, want 등록표 기본값 표기 %q", m.Key, info.Default, want)
		}
	}
	// 3. TUNING-table exposure — TuningKeyAll (the source of the list,
	// reset --all, and the TuningCurrent IN lookup) includes the whole registry.
	all := map[string]bool{}
	for _, k := range ops.TuningKeyAll() {
		all[k] = true
	}
	for _, m := range metas {
		if !all[m.Key] {
			t.Errorf("TUNING 표 노출 누락: %q", m.Key)
		}
	}
	km := ops.TuningKeyMap()
	for _, m := range metas {
		if _, ok := km[m.Key]; !ok {
			t.Errorf("TuningKeyMap 누락: %q", m.Key)
		}
	}
}

// TestNewKeyPropagates — registering 1 virtual key in the registry reflects
// simultaneously in the 4 paths (ThresholdsKeys, TuningKeyAll, the TuningKeyMap
// row, and the TuningKeys order), and removing it makes it vanish from all —
// demonstrating that a new key propagates with just 1 registry registration (+ a store struct field).
func TestNewKeyPropagates(t *testing.T) {
	probe := wire.ThresholdMeta{
		Field:   "PendingMax", // the virtual key also binds Get to a real field (the table-row contract)
		Key:     "zz_registry_probe",
		Default: 7,
		Desc:    "가드 테스트 가상 키",
	}
	restore := wire.ThresholdMetasAppendForTest(probe)

	inKeys := func(list []string) bool {
		for _, k := range list {
			if k == probe.Key {
				return true
			}
		}
		return false
	}
	// path 1: ThresholdsKeys (the thresholdsKeySQL IN, the maintain audit source).
	if !inKeys(wire.ThresholdsKeys()) {
		t.Error("ThresholdsKeys에 가상 키 미반영")
	}
	// path 2: TuningKeyAll (the TuningCurrent IN lookup, reset --all).
	if !inKeys(ops.TuningKeyAll()) {
		t.Error("TuningKeyAll에 가상 키 미반영")
	}
	// path 3: the TuningKeyMap row (the TUNING-table Default, Consumer, Get).
	info, ok := ops.TuningKeyMap()[probe.Key]
	if !ok {
		t.Fatal("TuningKeyMap에 가상 키 미반영")
	}
	if info.Default != "7" || info.Consumer != probe.Desc {
		t.Errorf("가상 키 표행: Default=%q Consumer=%q", info.Default, info.Consumer)
	}
	if got := info.Get(store.DefaultThresholds()); got != "50" {
		t.Errorf("가상 키 Get(실 필드 폴백)= %q, want 50", got)
	}
	// path 4: TuningKeys (the TUNING-table row-order source).
	if !func() bool {
		for _, k := range ops.TuningKeys() {
			if k.Key == probe.Key {
				return true
			}
		}
		return false
	}() {
		t.Error("TuningKeys에 가상 키 미반영")
	}

	// removing the registration — asserting erasure from every path.
	restore()
	if inKeys(wire.ThresholdsKeys()) || inKeys(ops.TuningKeyAll()) {
		t.Error("제거 후에도 키 잔류")
	}
	if _, ok := ops.TuningKeyMap()[probe.Key]; ok {
		t.Error("제거 후에도 TuningKeyMap 잔류")
	}
}

// fmtInt/fmtFloat — the registry-default notation (the same rules as the ops derivation).
func fmtInt(v int) string       { return strconv.Itoa(v) }
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
