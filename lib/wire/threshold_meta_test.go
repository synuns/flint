// threshold_meta_test.go — v0.42.0 T1(§2) 등록표 가드. Review Focus:
// TestThresholdRegistrySingleSource(등록표 키 집합 == 기존 키 집합·Parse
// 인식·tuningKeys·TUNING 표 노출 4곳 동질)·TestNewKeyPropagates(가상 키
// 1건 등록 시 4경로 동시 반영 — 등록 후 제거). wire 외부 테스트 패키지 —
// ops 파생 경로(TuningKeys) 검증을 위해 ops를 임포트한다.
package wire_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// frozenThresholdKeys — T1 이전 ThresholdsKeys 원본(순서 포함). 등록표
// 전환 후에도 키 집합·순서가 불변임을 단언하는 기준선.
var frozenThresholdKeys = []string{
	"pending_max", "dup_susp_max", "conf_floor", "stale_sessions",
	"stale_days", "stall_hot_h", "stall_write_h",
	"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
	"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
	"pending_age_days", "pending_promote_days", "pending_promote_min_runes", "invalidated_prune_days", "recall_warn_writes",
	"dead_days", "pair_window_minutes", "chain_window_hours",
	"events_retention_days", "hot_events_retention_days",
	"events_volume_warn", "drill_max_days", "daily_retention_days", "rrf_k", "secrets_scan_row_limit", "session_knowledge_days",
	// v1.2.0 T5: 진단 엔진·kreads 보존(diagnosis.go·TUNING §2)
	"diag_hot_budget_high", "diag_hot_budget_low", "diag_trend_pct", "diag_recall_led_goal", "diag_dead_warn", "knowledge_reads_retention_days",
}

// TestThresholdRegistrySingleSource — Review Focus. 등록표가 4곳(기존 키
// 집합·Parse 인식·tuningKeys·TUNING 표 노출)과 동질한지 — 등록표와
// store(구조체·기본값·Parse switch) 어느 한쪽에만 키를 추가해도 FAIL.
func TestThresholdRegistrySingleSource(t *testing.T) {
	metas := wire.ThresholdMetas()
	if len(metas) != len(frozenThresholdKeys) {
		t.Fatalf("등록표 항목 수: %d, want %d", len(metas), len(frozenThresholdKeys))
	}
	// 1. 기존 키 집합(순서 포함) — ThresholdsKeys는 등록표 파생이므로
	// 양방향 동질이 곧 파생 계약이다.
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
		// 기본값 동질 — 등록표 Default == DefaultThresholds 필드값.
		// Parse 인식 — 구분 가능한 센티널 값이 파싱 폴백 없이 반영되는지.
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
		}
	}
	// 2. tuningKeys 동질 — TuningKeys는 등록표 파생(순서·Default·
	// Consumer·Get 전부 등록표에서).
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
		}
		if info.Default != want {
			t.Errorf("%q: Default=%q, want 등록표 기본값 표기 %q", m.Key, info.Default, want)
		}
	}
	// 3. TUNING 표 노출 — TuningKeyAll(list·reset --all·TuningCurrent
	// IN 조회 소스)이 등록표 전체를 포함.
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

// TestNewKeyPropagates — Review Focus. 가상 키 1건을 등록표에 등록하면
// 4경로(ThresholdsKeys·TuningKeyAll·TuningKeyMap 행·TuningKeys 순서)가
// 동시 반영되고, 제거 시 전부에서 사라진다 — 신규 키는 등록표 1곳 등록
// (+store 구조체 필드)만으로 전파됨의 실증.
func TestNewKeyPropagates(t *testing.T) {
	probe := wire.ThresholdMeta{
		Field:   "PendingMax", // 가상 키도 실 필드에 Get을 바인딩(표 행 계약)
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
	// 경로 1: ThresholdsKeys(thresholdsKeySQL IN·maintain 감사 소스).
	if !inKeys(wire.ThresholdsKeys()) {
		t.Error("ThresholdsKeys에 가상 키 미반영")
	}
	// 경로 2: TuningKeyAll(TuningCurrent IN 조회·reset --all).
	if !inKeys(ops.TuningKeyAll()) {
		t.Error("TuningKeyAll에 가상 키 미반영")
	}
	// 경로 3: TuningKeyMap 행(TUNING 표 Default·Consumer·Get).
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
	// 경로 4: TuningKeys(TUNING 표 행 순서 소스).
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

	// 등록 제거 — 전 경로에서 소거 단언.
	restore()
	if inKeys(wire.ThresholdsKeys()) || inKeys(ops.TuningKeyAll()) {
		t.Error("제거 후에도 키 잔류")
	}
	if _, ok := ops.TuningKeyMap()[probe.Key]; ok {
		t.Error("제거 후에도 TuningKeyMap 잔류")
	}
}

// fmtInt/fmtFloat — 등록표 기본값 표기(ops 파생과 동일 규칙).
func fmtInt(v int) string       { return strconv.Itoa(v) }
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
