package wire_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var frozenThresholdKeys = []string{
	"pending_max", "dup_susp_max", "conf_floor", "stale_sessions",
	"stale_days", "stall_hot_h", "stall_write_h",
	"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
	"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
	"pending_age_days", "pending_promote_days", "pending_promote_min_runes", "invalidated_prune_days", "recall_warn_writes",
	"dead_days", "pair_window_minutes", "chain_window_hours",
	"events_retention_days", "hot_events_retention_days",
	"events_volume_warn", "drill_max_days", "daily_retention_days", "rrf_k", "secrets_scan_row_limit", "session_knowledge_days",

	"diag_hot_budget_high", "diag_hot_budget_low", "diag_trend_pct", "diag_recall_led_goal", "diag_dead_warn", "knowledge_reads_retention_days",

	"export_import_max_rows",

	"decay_rate", "archive_below",

	"recall_event_retention_days",

	"correction_confidence",

	"correction_daily_max",

	"gap_query_threshold", "gap_daily_max",

	"stale_score_threshold", "stale_ttl_days",

	"survived_decay_relief",

	"echo_top_k", "echo_min_score", "echo_topic_match",

	"weekly_rollups_retention_days", "threshold_snapshots_retention_days", "metering_freshness_warn_h",

	"vault_scan_max_files", "vault_summary_max_runes", "vault_skip_dirs",

	"distill_min_score", "distill_max_runes",

	"recall_audit_min_bad",

	"dolt_" + "log_depth_warn", "db_size_warn_mb",

	"dropped_dbs_warn", "stray_dbs_warn",

	"update_cache_age_h",

	"collection_freshness_hours", "plugin_version_gap_minors",

	"state_stale_days", "state_snapshot_max",

	"work_state_resolved_retention_days",
}

func TestStaleThresholdDefaultsOverride(t *testing.T) {

	def := store.DefaultThresholds()
	if def.StaleScoreThreshold != 70 {
		t.Errorf("StaleScoreThreshold 기본값=%d, want 70", def.StaleScoreThreshold)
	}
	if def.StaleTTLDays != 180 {
		t.Errorf("StaleTTLDays 기본값=%d, want 180", def.StaleTTLDays)
	}

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

	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.StaleScoreThreshold != 0 || unset.StaleTTLDays != 0 {
		t.Errorf("unset 폴백: stale_score_threshold=%d stale_ttl_days=%d, want 0·0", unset.StaleScoreThreshold, unset.StaleTTLDays)
	}
}

func TestGapThresholdDefaultsOverride(t *testing.T) {

	def := store.DefaultThresholds()
	if def.GapQueryThreshold != 3 {
		t.Errorf("GapQueryThreshold 기본값=%d, want 3", def.GapQueryThreshold)
	}
	if def.GapDailyMax != 10 {
		t.Errorf("GapDailyMax 기본값=%d, want 10", def.GapDailyMax)
	}

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

	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.GapQueryThreshold != 0 || unset.GapDailyMax != 0 {
		t.Errorf("unset 폴백: gap_query_threshold=%d gap_daily_max=%d, want 0·0", unset.GapQueryThreshold, unset.GapDailyMax)
	}
}

func TestSurvivedReliefThresholdDefaultsOverride(t *testing.T) {

	def := store.DefaultThresholds()
	if def.SurvivedDecayRelief != 0 {
		t.Errorf("SurvivedDecayRelief 기본값=%v, want 0(비활성)", def.SurvivedDecayRelief)
	}

	th, err := store.ParseThresholds([][2]any{
		{"survived_decay_relief", "1.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.SurvivedDecayRelief != 1.5 {
		t.Errorf("survived_decay_relief 오버라이드=%v, want 1.5", th.SurvivedDecayRelief)
	}

	neg, err := store.ParseThresholds([][2]any{
		{"survived_decay_relief", "-0.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.SurvivedDecayRelief != 0 {
		t.Errorf("음수 거부=%v, want 0(폴백)", neg.SurvivedDecayRelief)
	}

	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.SurvivedDecayRelief != 0 {
		t.Errorf("unset 폴백: survived_decay_relief=%v, want 0", unset.SurvivedDecayRelief)
	}
}

func TestEchoThresholdDefaultsOverride(t *testing.T) {

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

	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.EchoTopK != 0 || unset.EchoMinScore != 0 || unset.EchoTopicMatch != 0 {
		t.Errorf("unset 폴백: echo_top_k=%d echo_min_score=%v echo_topic_match=%v, want 0·0·0",
			unset.EchoTopK, unset.EchoMinScore, unset.EchoTopicMatch)
	}
}

func TestThresholdRegistrySingleSource(t *testing.T) {
	metas := wire.ThresholdMetas()
	if len(metas) != len(frozenThresholdKeys) {
		t.Fatalf("등록표 항목 수: %d, want %d", len(metas), len(frozenThresholdKeys))
	}

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

func TestNewKeyPropagates(t *testing.T) {
	probe := wire.ThresholdMeta{
		Field:   "PendingMax",
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

	if !inKeys(wire.ThresholdsKeys()) {
		t.Error("ThresholdsKeys에 가상 키 미반영")
	}

	if !inKeys(ops.TuningKeyAll()) {
		t.Error("TuningKeyAll에 가상 키 미반영")
	}

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

	restore()
	if inKeys(wire.ThresholdsKeys()) || inKeys(ops.TuningKeyAll()) {
		t.Error("제거 후에도 키 잔류")
	}
	if _, ok := ops.TuningKeyMap()[probe.Key]; ok {
		t.Error("제거 후에도 TuningKeyMap 잔류")
	}
}

func TestApplyThresholdsFallbackSingleSource(t *testing.T) {
	restore := wire.ThresholdMetasReplaceForTest(store.ThresholdMeta{
		Field: "StaleDays", Key: "stale_days", Default: 77,
		Type: "int", Fallback: true, Desc: "t3 fallback probe",
	})

	fb := wire.ApplyThresholdsFallback(store.Thresholds{})
	if fb.StaleDays != 77 {
		t.Errorf("교체 행 폴백: stale_days=%d, want 77", fb.StaleDays)
	}
	kept := wire.ApplyThresholdsFallback(store.Thresholds{StaleDays: 5})
	if kept.StaleDays != 5 {
		t.Errorf("0이 아닌 값 유지: stale_days=%d, want 5", kept.StaleDays)
	}
	neg := wire.ApplyThresholdsFallback(store.Thresholds{StaleDays: -3})
	if neg.StaleDays != 77 {
		t.Errorf("int 음수 폴백: stale_days=%d, want 77", neg.StaleDays)
	}

	restore()
	fb2 := wire.ApplyThresholdsFallback(store.Thresholds{})
	if fb2.StaleDays != 90 {
		t.Errorf("복원 후 폴백: stale_days=%d, want 90", fb2.StaleDays)
	}
}

func TestApplyThresholdsFallbackRegistryScope(t *testing.T) {
	fb := wire.ApplyThresholdsFallback(store.Thresholds{})
	if fb.PendingMax != 0 {
		t.Errorf("폴백 미대상 침범: pending_max=%d, want 0", fb.PendingMax)
	}
	if fb.EchoTopK != 0 {
		t.Errorf("폴백 미대상 침범: echo_top_k=%d, want 0", fb.EchoTopK)
	}
	if fb.VaultSkipDirs != "" {
		t.Errorf("폴백 미대상 침범: vault_skip_dirs=%q, want \"\"", fb.VaultSkipDirs)
	}
	if fb.DroppedDBsWarn != 1 || fb.StrayDBsWarn != 0 || fb.UpdateCacheAgeH != 72 {
		t.Errorf("폴백 대상 누락: dropped=%d stray=%d update=%d, want 1·0·72",
			fb.DroppedDBsWarn, fb.StrayDBsWarn, fb.UpdateCacheAgeH)
	}
}

func TestThresholdAutoWhitelist(t *testing.T) {
	grades := map[string]bool{"": true, "shadow": true}
	want := map[string]string{"suggest_min_score": "shadow"}
	got := map[string]string{}
	for _, m := range wire.ThresholdMetas() {
		if !grades[m.Auto] {
			t.Errorf("%q: 미지원 auto 등급 %q (폐쇄 집합 \"\"|shadow)", m.Key, m.Auto)
		}
		if m.Auto != "" {
			got[m.Key] = m.Auto
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auto 화이트리스트 드리프트:\n got=%v\nwant=%v", got, want)
	}
	if g := wire.ThresholdAutoGrade("suggest_min_score"); g != "shadow" {
		t.Errorf("ThresholdAutoGrade(suggest_min_score)=%q, want shadow", g)
	}
	if g := wire.ThresholdAutoGrade("pending_max"); g != "" {
		t.Errorf("ThresholdAutoGrade(pending_max)=%q, want \"\"(수동 전용)", g)
	}
	if g := wire.ThresholdAutoGrade("zz_unknown_key"); g != "" {
		t.Errorf("ThresholdAutoGrade(미지정 키)=%q, want \"\"", g)
	}
	if w := wire.ThresholdAutoWhitelist(); !reflect.DeepEqual(w, want) {
		t.Fatalf("ThresholdAutoWhitelist:\n got=%v\nwant=%v", w, want)
	}
}

func fmtInt(v int) string       { return strconv.Itoa(v) }
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func TestEngineWarnThresholdOverride(t *testing.T) {
	def := store.DefaultThresholds()
	if def.DoltLogDepthWarn != 10000 || def.DbSizeWarnMB != 2048 {
		t.Fatalf("기본값: depth=%d size=%d, want 10000·2048",
			def.DoltLogDepthWarn, def.DbSizeWarnMB)
	}
	th, err := store.ParseThresholds([][2]any{
		{"dolt_" + "log_depth_warn", "5000"},
		{"db_size_warn_mb", "4096"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DoltLogDepthWarn != 5000 || th.DbSizeWarnMB != 4096 {
		t.Errorf("오버라이드: depth=%d size=%d, want 5000·4096",
			th.DoltLogDepthWarn, th.DbSizeWarnMB)
	}

	fb := wire.ApplyThresholdsFallback(store.Thresholds{})
	if fb.DoltLogDepthWarn != 10000 || fb.DbSizeWarnMB != 2048 {
		t.Errorf("폴백: depth=%d size=%d, want 10000·2048",
			fb.DoltLogDepthWarn, fb.DbSizeWarnMB)
	}
}
