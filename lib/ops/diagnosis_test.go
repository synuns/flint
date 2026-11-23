package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

func diagFixture() ReportOutput {
	var out ReportOutput
	out.Effect.Bytes.Hot = SurfaceBytes{Bytes14d: 1000, AvgBytes: 1000}
	out.Effect.ReadEventsN = 100
	out.ZeroNearPct = 5
	out.Effect.DeadPct = 2
	out.Effect.RecallLedPct = 80
	out.Effect.Cost.SavingsLowerBoundBytes = 1000
	out.Effect.Cost.HitInjectionCostBytes = 500
	out.Days = []ReportDay{
		{Date: "2026-11-12", HotBytes: 500},
		{Date: "2026-11-13", HotBytes: 500},
	}
	return out
}

func TestDiagnoseRules(t *testing.T) {
	th := store.DefaultThresholds()
	const budget = 2000

	has := func(items []DiagnosisItem, signal string) bool {
		for _, it := range items {
			if it.Signal == signal {
				return true
			}
		}
		return false
	}

	if got := Diagnose(diagFixture(), th, budget); len(got) != 0 {
		t.Errorf("정상 입력 진단 = %v, want 빈 배열", got)
	}

	cases := []struct {
		name   string
		mutate func(*ReportOutput)
		signal string
	}{
		{"A1 hot 예산 포화(≥0.8) — 만성 절단", func(o *ReportOutput) {
			o.Effect.Bytes.Hot = SurfaceBytes{Bytes14d: 1800, AvgBytes: 1800}
		}, "hot_budget_saturation"},
		{"A2 hot 예산 여유(≤0.2) — 정보성", func(o *ReportOutput) {
			o.Effect.Bytes.Hot = SurfaceBytes{Bytes14d: 200, AvgBytes: 200}
		}, "hot_budget_slack"},
		{"A3 hot bytes 추세 +200%(≥50%)", func(o *ReportOutput) {
			o.Days = []ReportDay{{Date: "2026-11-12", HotBytes: 100}, {Date: "2026-11-13", HotBytes: 300}}
		}, "hot_bytes_trend"},
		{"A4 주입 원가 > 절약 하한", func(o *ReportOutput) {
			o.Effect.Cost.HitInjectionCostBytes = 1200
		}, "hit_cost_exceeds_savings"},
		{"B1 zero_near 40% ≥ doctor 값 30 승계", func(o *ReportOutput) {
			o.ZeroNearPct = 40
		}, "zero_near_high"},
		{"B2 dead 25% ≥ diag_dead_warn 10", func(o *ReportOutput) {
			o.Effect.DeadPct = 25
		}, "dead_knowledge"},
		{"B3 pending 적체 — doctor pending_age 기준 승계", func(o *ReportOutput) {
			o.Counts.Pending = 3
		}, "pending_backlog"},
		{"C1 recall_led 30% < 기본 목표 50", func(o *ReportOutput) {
			o.Effect.RecallLedPct = 30
		}, "recall_led_below_goal"},
	}
	for _, tc := range cases {
		out := diagFixture()
		tc.mutate(&out)
		if !has(Diagnose(out, th, budget), tc.signal) {
			t.Errorf("%s: 진단에 %q 없음", tc.name, tc.signal)
		}

		if has(Diagnose(diagFixture(), th, budget), tc.signal) {
			t.Errorf("%s: 정상 입력에서 %q 오탐", tc.name, tc.signal)
		}
	}

	mid := diagFixture()
	mid.Effect.DeadPct = 9
	mid.ZeroNearPct = 29
	mid.Effect.RecallLedPct = 50
	mid.Days = []ReportDay{{Date: "2026-11-12", HotBytes: 100}, {Date: "2026-11-13", HotBytes: 120}}
	if got := Diagnose(mid, th, budget); len(got) != 0 {
		t.Errorf("경계 미달 입력 진단 = %v, want 빈 배열", got)
	}

	sparse := diagFixture()
	sparse.ZeroNearPct = 100
	sparse.Effect.RecallLedPct = 0
	sparse.Effect.ReadEventsN = 5
	if got := Diagnose(sparse, th, budget); len(got) != 0 {
		t.Errorf("표본 부족 입력 진단 = %v, want 빈 배열(판정 보류)", got)
	}

	zero := diagFixture()
	zero.Effect.DeadPct = 25
	if !has(Diagnose(zero, store.Thresholds{}, budget), "dead_knowledge") {
		t.Error("미설정 th에서 dead 규칙 미발동 — 기본값 폴백 계약 위반")
	}

	noHot := diagFixture()
	noHot.Effect.Bytes.Hot = SurfaceBytes{}
	if got := Diagnose(noHot, th, budget); len(got) != 0 {
		t.Errorf("hot 호출 0 입력 진단 = %v, want 빈 배열", got)
	}

	newHot := diagFixture()
	newHot.Days = []ReportDay{{Date: "2026-11-12"}, {Date: "2026-11-13", HotBytes: 900}}
	if got := Diagnose(newHot, th, budget); len(got) != 0 {
		t.Errorf("전반 0 입력 진단 = %v, want 빈 배열", got)
	}

	noSavings := diagFixture()
	noSavings.Effect.Cost.SavingsLowerBoundBytes = 0
	if got := Diagnose(noSavings, th, budget); len(got) != 0 {
		t.Errorf("절약 하한 0 입력 진단 = %v, want 빈 배열", got)
	}
}

func TestDiagnosisSentenceContract(t *testing.T) {
	th := store.DefaultThresholds()
	out := diagFixture()
	out.Effect.Bytes.Hot = SurfaceBytes{Bytes14d: 1800, AvgBytes: 1800}
	out.Effect.DeadPct = 25
	out.Effect.RecallLedPct = 30
	items := Diagnose(out, th, 2000)
	if len(items) != 3 {
		t.Fatalf("진단 항목 수=%d, want 3(A1·B2·C1)", len(items))
	}
	bySignal := map[string]DiagnosisItem{}
	for _, it := range items {
		bySignal[it.Signal] = it
	}
	for signal, wantBasis := range map[string]string{
		"hot_budget_saturation": "diag_hot_budget_high=0.8",
		"dead_knowledge":        "diag_dead_warn=10",
		"recall_led_below_goal": "diag_recall_led_goal=50",
	} {
		it, ok := bySignal[signal]
		if !ok {
			t.Fatalf("%q 항목 없음: %v", signal, items)
		}
		s := it.Sentence()
		for _, part := range []string{it.Signal, it.Value, it.Basis, it.Action} {
			if part == "" {
				t.Errorf("%q: 빈 필드(value/basis/action 계약)", signal)
			}
			if !strings.Contains(s, part) {
				t.Errorf("%q 문장이 %q 미포함: %q", signal, part, s)
			}
		}
		if !strings.Contains(it.Basis, wantBasis) {
			t.Errorf("%q basis=%q, 기준 출처 %q 미포함", signal, it.Basis, wantBasis)
		}
	}

	if it := bySignal["recall_led_below_goal"]; !strings.Contains(it.Basis, "default goal") {
		t.Errorf("C계층 basis=%q, '기본 목표' 라벨 미포함", it.Basis)
	}

	if it := bySignal["hot_budget_saturation"]; !strings.Contains(it.Action, "chronic truncation") {
		t.Errorf("A1 action=%q, '만성 절단' 라벨 미포함", it.Action)
	}

	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"signal"`, `"value"`, `"basis"`, `"action"`, `"tuning_key"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("진단 JSON에 필드 키 %s 없음: %s", key, b)
		}
	}

	zn := diagFixture()
	zn.ZeroNearPct = 40
	items = Diagnose(zn, th, 2000)
	if len(items) != 1 || !strings.Contains(items[0].Basis, "zero_near_warn_pct") ||
		items[0].TuningKey != "zero_near_warn_pct" {
		t.Fatalf("zero_near 진단 = %v, doctor 키 승계 계약 위반", items)
	}
}

func TestDiagnosisReportExposure(t *testing.T) {
	_, today := v1200Today()

	s := &v1200Store{daily: map[string]string{
		"daily:read:" + today: "1", "daily:hot:" + today: "1",
		"daily:hot_bytes:" + today: "1900",
	}}
	m := runReportV1200(t, s, "--days", "1")
	arr, ok := m["diagnosis"].([]any)
	if !ok {
		t.Fatalf("diagnosis 배열 없음: %v", m)
	}
	if len(arr) != 1 {
		t.Fatalf("diagnosis 항목 수=%d, want 1(A1)", len(arr))
	}
	d := arr[0].(map[string]any)
	if d["signal"] != "hot_budget_saturation" {
		t.Errorf("diagnosis[0].signal=%v, want hot_budget_saturation", d["signal"])
	}

	s2 := &v1200Store{daily: map[string]string{
		"daily:read:" + today: "1", "daily:hot:" + today: "1",
		"daily:hot_bytes:" + today: "1000",
	}}
	m2 := runReportV1200(t, s2, "--days", "1")
	arr2, ok := m2["diagnosis"].([]any)
	if !ok || len(arr2) != 0 {
		t.Errorf("정상 diagnosis=%v(%T), want 빈 배열", m2["diagnosis"], m2["diagnosis"])
	}

	items := Diagnose(func() ReportOutput {
		o := diagFixture()
		o.Effect.DeadPct = 25
		return o
	}(), store.DefaultThresholds(), 2000)
	if len(items) != 1 {
		t.Fatalf("HTML용 진단 항목 수=%d, want 1", len(items))
	}
	var buf strings.Builder
	if err := RenderDashboard(&buf, HtmlDashboard{Rep: ReportOutput{Diagnosis: items}}); err != nil {
		t.Fatalf("RenderDashboard: %v", err)
	}
	if !strings.Contains(buf.String(), items[0].Signal) || !strings.Contains(buf.String(), items[0].Value) {
		t.Errorf("HTML 카드에 진단 항목 미렌더: %s", buf.String())
	}
	var quiet strings.Builder
	if err := RenderDashboard(&quiet, HtmlDashboard{Rep: ReportOutput{Diagnosis: []DiagnosisItem{}}}); err != nil {
		t.Fatalf("RenderDashboard(정상): %v", err)
	}
	if strings.Contains(quiet.String(), "진단") {
		t.Error("정상(빈 진단) HTML에 진단 섹션 렌더 — 정상 침묵 위반")
	}
}

var frozenMeteringKindGroups = map[string][]string{
	"usage": {"write", "hot", "read", "dup", "dup_hook", "dup_manual", "dup_mcp", "dup_correction", "dup_gap", "brief"},
	"curation": {"approve_done", "approve_not_found", "approve_supersede_err",
		"maintain_supersede", "maintain_expire", "maintain_prune_rows",
		"import_added", "import_skipped", "correction_approved", "correction_preference", "correction_fact",
		"gap_approved", "survived_sessions", "merge_audit_pairs",
		"recall_audit_listed", "recall_audit_disposed"},
	"bytes": {"read", "hot", "brief"},
}

func TestMeteringKindRegistryHomogeneity(t *testing.T) {
	total := 0
	for group, want := range frozenMeteringKindGroups {
		got := db.MeteringKinds(group)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("그룹 %s 종류 = %v, want %v(집합·순서 불변)", group, got, want)
		}
		total += len(want)
	}
	if total != 29 {
		t.Errorf("합집합 %d키, want 29키", total)
	}
	if db.MeteringKinds("bogus") != nil {
		t.Error("미지 그룹에 비nil 반환 — 그룹 구분 붕괴 소지")
	}
}

func silentDaysFixture(n int) []ReportDay {
	days := make([]ReportDay, n)
	for i := range days {
		days[i] = ReportDay{
			Date:  fmt.Sprintf("2026-10-%02d", i+1),
			Write: 2, Hook: 1, Manual: 1, Mcp: 0, Dup: 1,
			DupHook: 1, DupManual: 1, DupMcp: 1, DupCorrection: 1, DupGap: 1,
			Hot: 2, Read: 5, Brief: 1,
			ReadBytes: 100, HotBytes: 100, BriefBytes: 50,
		}
	}
	return days
}

func TestDiagnoseMeteringSilent(t *testing.T) {
	th := store.DefaultThresholds()

	hasSilent := func(items []DiagnosisItem) *DiagnosisItem {
		for i := range items {
			if items[i].Signal == "metering_silent" {
				return &items[i]
			}
		}
		return nil
	}

	curationFired := CurationCounters{
		ApproveDone: 1, ApproveNotFound: 1, ApproveSupersedeErr: 1,
		MaintainSupersede: 1, MaintainExpire: 1, MaintainPruneRows: 1,
		ImportAdded: 1, ImportSkipped: 1,
	}
	out := diagFixture()
	out.Days = silentDaysFixture(30)
	out.Curation = curationFired
	if it := hasSilent(Diagnose(out, th, 2000)); it != nil {
		t.Fatalf("전 종 발화 창에서 silent 항목: %+v", it)
	}

	out = diagFixture()
	out.Days = silentDaysFixture(30)
	out.Curation = curationFired
	for i := range out.Days {
		out.Days[i].DupMcp = 0
		out.Days[i].ReadBytes = 0
	}
	it := hasSilent(Diagnose(out, th, 2000))
	if it == nil {
		t.Fatal("미발화 2종인데 silent 항목 부재")
	}
	if !strings.Contains(it.Value, "usage:dup_mcp") || !strings.Contains(it.Value, "bytes:read") {
		t.Fatalf("silent 값 %q — 그룹 한정 종료명 미포함", it.Value)
	}

	out = diagFixture()
	out.Days = silentDaysFixture(7)
	for i := range out.Days {
		out.Days[i].DupMcp = 0
	}
	if it := hasSilent(Diagnose(out, th, 2000)); it != nil {
		t.Fatalf("7일 창에서 silent 항목 — 장기 임계(30일) 미적용: %+v", it)
	}
}
