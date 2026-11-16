// report_test.go — v1.5.0 T9 report TUI 모델 테스트. 구조 단언(렌더
// 스냅샷 없음 — T5 관례): 진단 카드 수·제목·상세 확장·지표 뷰 전환·
// 종료 키. 픽스처는 합성 데모 데이터(저장소 원칙).
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/ops"
)

// diagFixture — 진단 2항목(경고 1·정보 1)과 최소 지표를 담은 합성
// ReportOutput. 진단 문장 값은 TUI가 원천 그대로 노출하는지만 본다.
func diagFixture() ops.ReportOutput {
	rep := ops.ReportOutput{
		Days: []ops.ReportDay{
			{Date: "2026-11-14", Write: 3, Read: 10, Dup: 1},
			{Date: "2026-11-15", Write: 2, Read: 8, Dup: 1},
		},
		ZeroPct: 10,
		HotP95:  400,
	}
	rep.Counts.Pending = 2
	rep.Counts.Active = 5
	rep.Counts.Invalidated = 1
	rep.DupPct = 12
	rep.FallbackPct = 5
	rep.Effect.HitPct = 90
	rep.Diagnosis = []ops.DiagnosisItem{
		{
			Signal:    "hot_budget_saturation",
			Value:     "hot injection avg 1800 bytes = 90% of budget",
			Basis:     "diag_hot_budget_high=0.8(ratio of hot avg to hot_budget)",
			Action:    "consider raising hot_budget",
			TuningKey: "hot_budget",
		},
		{
			Signal: "hot_budget_slack",
			Value:  "hot injection avg 200 bytes = 10% of budget",
			Basis:  "diag_hot_budget_low=0.2(ratio of hot avg to hot_budget)",
			Action: "informational — headroom is large",
		},
	}
	return rep
}

// updateKey — 모델에 키 입력 1개를 넣고 ReportModel로 되돌리는 헬퍼.
func updateKey(t *testing.T, m ReportModel, key tea.KeyType) ReportModel {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: key})
	mm, ok := next.(ReportModel)
	if !ok {
		t.Fatalf("Update returned %T, want ReportModel", next)
	}
	if cmd != nil {
		_ = cmd() // tea.Quit 여부는 별도 단언
	}
	return mm
}

// TestReportModelCardList — 진단 항목 수·카드 제목 렌더: 항목 2개면
// 신호 제목 2개가 모두 보인다(진단 뷰 기본).
func TestReportModelCardList(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	v := m.View()
	for _, want := range []string{"hot_budget_saturation", "hot_budget_slack"} {
		if !strings.Contains(v, want) {
			t.Errorf("card list missing signal %q", want)
		}
	}
	if !strings.Contains(v, "Diagnosis") {
		t.Errorf("view missing Diagnosis header: %q", v)
	}
}

// TestReportModelDetailExpand — 선택 항목 확장(enter) 시 Basis·Action·
// tuning key 필드가 Value와 함께 보인다(계약: 선택 상세).
func TestReportModelDetailExpand(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	m = updateKey(t, m, tea.KeyEnter)
	v := m.View()
	for _, want := range []string{
		"Basis:", "diag_hot_budget_high=0.8",
		"Action:", "consider raising hot_budget",
		"Tuning key:", "hot_budget",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("expanded detail missing %q", want)
		}
	}
}

// TestReportModelMetricsView — tab으로 지표 요약 뷰: 창 총적립·회상·
// 핵심 비율·counts가 보인다(계약: 지표 요약 탐색).
func TestReportModelMetricsView(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	m = updateKey(t, m, tea.KeyTab)
	v := m.View()
	for _, want := range []string{
		"Metrics",
		"writes 5",  // Days 합산
		"reads 18",  // Days 합산
		"hit 90%",   // effect.hit_pct
		"pending 2", // counts.pending
	} {
		if !strings.Contains(v, want) {
			t.Errorf("metrics view missing %q", want)
		}
	}
}

// TestReportModelEmptyDiagnosis — 진단 0건은 정상 침묵 대응 메시지
// (지표 뷰는 여전히 탐색 가능 — 빈 화면 금지).
func TestReportModelEmptyDiagnosis(t *testing.T) {
	rep := diagFixture()
	rep.Diagnosis = nil
	m := NewReportModel(rep, 7)
	v := m.View()
	if !strings.Contains(v, "No diagnosis") {
		t.Errorf("empty diagnosis should render a no-findings message: %q", v)
	}
	m = updateKey(t, m, tea.KeyTab)
	if !strings.Contains(m.View(), "Metrics") {
		t.Error("metrics view should remain reachable with empty diagnosis")
	}
}

// TestReportModelCursorMoveAndDetail — 커서 이동(j/k) 후 확장은 커서가
// 가리키는 항목의 상세를 보인다(두 번째 항목).
func TestReportModelCursorMoveAndDetail(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	m = updateKey(t, m, tea.KeyDown)
	m = updateKey(t, m, tea.KeyEnter)
	v := m.View()
	if !strings.Contains(v, "diag_hot_budget_low=0.2") {
		t.Errorf("detail should show second item basis after cursor down: %q", v)
	}
}

// TestReportModelQuitKeys — q·esc 종료 관례(T5 계약).
func TestReportModelQuitKeys(t *testing.T) {
	// tea.Quit는 Cmd(func)라 직접 비교 불가 — 실행 산물 tea.QuitMsg
	// 타입 단언으로 판정한다.
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		_, cmd := NewReportModel(diagFixture(), 7).Update(tea.KeyMsg{Type: key})
		if cmd == nil {
			t.Errorf("key %v: cmd=nil, want tea.Quit", key)
			continue
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("key %v: cmd() is not tea.QuitMsg", key)
		}
	}
	// 'q' 룬키도 종료.
	_, cmd := NewReportModel(diagFixture(), 7).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("rune 'q' should quit")
	}
}

// updateSize — 리사이즈 시뮬레이션 헬퍼(report 모델 — 폭 저장).
func updateSize(m ReportModel, w int) ReportModel {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
	mm, ok := next.(ReportModel)
	if !ok {
		panic("Update returned non-ReportModel")
	}
	return mm
}

// RF-narrow — 계약 1: 40컬 — 두 모드 모두 키 안내 라인이 폭 안에 들어오되
// "q quit" 단서는 유지되고, 탈출 안내(nonInteractiveHint)는 wrap된다.
func TestReportNarrowWidthClipping(t *testing.T) {
	views := map[string]string{
		"diagnosis": updateSize(NewReportModel(diagFixture(), 7), 40).View(),
		"metrics":   updateKey(t, updateSize(NewReportModel(diagFixture(), 7), 40), tea.KeyTab).View(),
	}
	for name, v := range views {
		if !strings.Contains(v, "q quit") {
			t.Errorf("%s: 40-col view lost 'q quit'\nview:\n%s", name, v)
			continue
		}
		for _, ln := range strings.Split(stripANSI(v), "\n") {
			if strings.Contains(ln, "q quit") || strings.Contains(ln, "pipe output") {
				if n := len([]rune(ln)); n > 40 {
					t.Errorf("%s: hint line %d chars > 40: %q", name, n, ln)
				}
			}
		}
	}
}

// TestSeverityOf — 심각도 파생 매핑: 유일한 '정보성' 항목(hot_budget_slack
// — 진단 문구 계약상 informational)은 info, 나머지는 warn. DiagnosisItem에
// 심각도 필드가 없어 TUI가 신호 id로 파생한다(가정 로그 참조).
func TestSeverityOf(t *testing.T) {
	if got := severityOf("hot_budget_slack"); got != "info" {
		t.Errorf("hot_budget_slack severity=%q, want info", got)
	}
	for _, sig := range []string{
		"hot_budget_saturation", "hot_bytes_trend", "hit_cost_exceeds_savings",
		"zero_near_high", "dead_knowledge", "pending_backlog", "recall_led_below_goal",
	} {
		if got := severityOf(sig); got != "warn" {
			t.Errorf("%s severity=%q, want warn", sig, got)
		}
	}
}
