// diagnosis_test.go — v1.2.0 T5 규칙 진단 D 회귀(Review Focus):
// TestDiagnoseRules(규칙 8항목 트리거/비트리거·정상 침묵·미설정 키 폴백)·
// TestDiagnosisSentenceContract(문장 계약 — 신호+수치+기준 출처+행동)·
// TestDiagnosisReportExposure(세 뷰 동일 원천 — report JSON 배열·HTML 카드
// 모두 ReportOutput.Diagnosis에서 렌더).
package ops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// diagFixture — 규칙 판정 입력 기본값(정상 — 어느 규칙도 트리거하지 않는다).
// hot 평균 1000/상한 2000(50%·밴드 중간)·추세 0%·절약 하한이 주입 원가보다
// 큼·zero_near 5%(표본 100)·dead 2%·pending 0·recall_led 80.
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

// TestDiagnoseRules — Review Focus: 각 규칙 트리거/비트리거·정상 침묵(빈
// 배열)·미설정(0) thresholds의 기본값 폴백. 규칙 수 계약: A계층 4(hot
// 예산 밴드 고/저 2항목·bytes 추세·hit 원가 vs 절약 하한)·B계층 3(zero_near
// 승계·dead·pending 승계)·C계층 1(recall_led 기본 목표) = 8항목(규칙 7종
// — hot 예산 밴드는 하나의 규칙 양방 판정).
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

	// 정상 침묵 — 문제 없으면 빈 배열(스펙 §3 정상 침묵 유효).
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
		// 비트리거 — 원본(정상) 입력에는 해당 신호가 없다.
		if has(Diagnose(diagFixture(), th, budget), tc.signal) {
			t.Errorf("%s: 정상 입력에서 %q 오탐", tc.name, tc.signal)
		}
	}

	// 트리거 경계 근처 비트리거 — 밴드 중간·완만 추세·원가 우위.
	mid := diagFixture()
	mid.Effect.DeadPct = 9
	mid.ZeroNearPct = 29
	mid.Effect.RecallLedPct = 50
	mid.Days = []ReportDay{{Date: "2026-11-12", HotBytes: 100}, {Date: "2026-11-13", HotBytes: 120}}
	if got := Diagnose(mid, th, budget); len(got) != 0 {
		t.Errorf("경계 미달 입력 진단 = %v, want 빈 배열", got)
	}

	// 표본 하한 승계 — zero_near·recall_led 모두 read 표본 < 10이면 판정 보류.
	sparse := diagFixture()
	sparse.ZeroNearPct = 100
	sparse.Effect.RecallLedPct = 0
	sparse.Effect.ReadEventsN = 5
	if got := Diagnose(sparse, th, budget); len(got) != 0 {
		t.Errorf("표본 부족 입력 진단 = %v, want 빈 배열(판정 보류)", got)
	}

	// 미설정(0) thresholds — 소비자 폴백으로 기본값 판정(dead 25 트리거).
	zero := diagFixture()
	zero.Effect.DeadPct = 25
	if !has(Diagnose(zero, store.Thresholds{}, budget), "dead_knowledge") {
		t.Error("미설정 th에서 dead 규칙 미발동 — 기본값 폴백 계약 위반")
	}

	// 제로 가드 — hot 호출 0(avg 0)이면 예산 밴드 판정하지 않는다.
	noHot := diagFixture()
	noHot.Effect.Bytes.Hot = SurfaceBytes{}
	if got := Diagnose(noHot, th, budget); len(got) != 0 {
		t.Errorf("hot 호출 0 입력 진단 = %v, want 빈 배열", got)
	}
	// 추세 제로 가드 — 전반 합 0이면 비교 없음(신규 사용 등).
	newHot := diagFixture()
	newHot.Days = []ReportDay{{Date: "2026-11-12"}, {Date: "2026-11-13", HotBytes: 900}}
	if got := Diagnose(newHot, th, budget); len(got) != 0 {
		t.Errorf("전반 0 입력 진단 = %v, want 빈 배열", got)
	}
	// 절약 하한 0(비교 원천 없음)이면 원가 비교하지 않는다.
	noSavings := diagFixture()
	noSavings.Effect.Cost.SavingsLowerBoundBytes = 0
	if got := Diagnose(noSavings, th, budget); len(got) != 0 {
		t.Errorf("절약 하한 0 입력 진단 = %v, want 빈 배열", got)
	}
}

// TestDiagnosisSentenceContract — Review Focus: 문장 계약(스펙 §3) — 각
// 항목의 문장 렌더는 신호+수치+기준 출처+행동을 1줄에 포함한다. 기준
// 출처는 계층을 드러낸다(A 자기참조 diag_*·B doctor 승계·C 가치 기본값).
func TestDiagnosisSentenceContract(t *testing.T) {
	th := store.DefaultThresholds()
	out := diagFixture()
	out.Effect.Bytes.Hot = SurfaceBytes{Bytes14d: 1800, AvgBytes: 1800} // A1
	out.Effect.DeadPct = 25                                             // B2
	out.Effect.RecallLedPct = 30                                        // C1
	items := Diagnose(out, th, 2000)
	if len(items) != 3 {
		t.Fatalf("진단 항목 수=%d, want 3(A1·B2·C1)", len(items))
	}
	bySignal := map[string]DiagnosisItem{}
	for _, it := range items {
		bySignal[it.Signal] = it
	}
	for signal, wantBasis := range map[string]string{
		"hot_budget_saturation": "diag_hot_budget_high=0.8", // A — 자기참조 키
		"dead_knowledge":        "diag_dead_warn=10",        // B diag 키
		"recall_led_below_goal": "diag_recall_led_goal=50",  // C — 가치 기본값 라벨
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
	// C계층 라벨 — 가치 기본값(습관 목표) 표기.
	if it := bySignal["recall_led_below_goal"]; !strings.Contains(it.Basis, "기본 목표") {
		t.Errorf("C계층 basis=%q, '기본 목표' 라벨 미포함", it.Basis)
	}
	// A1 만성 절단 라벨.
	if it := bySignal["hot_budget_saturation"]; !strings.Contains(it.Action, "만성 절단") {
		t.Errorf("A1 action=%q, '만성 절단' 라벨 미포함", it.Action)
	}
	// JSON 배열 노출 — 항목은 5필드 구조로 직렬화된다(단일 데이터 모델).
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"signal"`, `"value"`, `"basis"`, `"action"`, `"tuning_key"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("진단 JSON에 필드 키 %s 없음: %s", key, b)
		}
	}
	// B계층 승계 표기 — zero_near는 doctor 키를 기준 출처로 인용한다(중복 키 방지).
	zn := diagFixture()
	zn.ZeroNearPct = 40
	items = Diagnose(zn, th, 2000)
	if len(items) != 1 || !strings.Contains(items[0].Basis, "zero_near_warn_pct") ||
		items[0].TuningKey != "zero_near_warn_pct" {
		t.Fatalf("zero_near 진단 = %v, doctor 키 승계 계약 위반", items)
	}
}

// TestDiagnosisReportExposure — Review Focus: 세 뷰 동일 원천(사용자 원칙 —
// 뷰 간 데이터 분열 금지). (a) report --json: diagnosis 배열이 ReportOutput
// 에서 직렬화된다. (b) 정상 침묵: 빈 배열(null 아님). (c) HTML: 같은
// ReportOutput.Diagnosis를 카드로 렌더(빈 경우 섹션 자체가 없다).
func TestDiagnosisReportExposure(t *testing.T) {
	_, today := v1200Today()
	// (a) hot 평균 1900/상한 2000(95%) — A1 트리거 픽스처.
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
	// (b) 정상 침묵 — 빈 배열(문제 없을 때 null이 아닌 []).
	s2 := &v1200Store{daily: map[string]string{
		"daily:read:" + today: "1", "daily:hot:" + today: "1",
		"daily:hot_bytes:" + today: "1000",
	}}
	m2 := runReportV1200(t, s2, "--days", "1")
	arr2, ok := m2["diagnosis"].([]any)
	if !ok || len(arr2) != 0 {
		t.Errorf("정상 diagnosis=%v(%T), want 빈 배열", m2["diagnosis"], m2["diagnosis"])
	}
	// (c) HTML — 같은 항목을 카드로 렌더·정상 침묵은 섹션 없음.
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
