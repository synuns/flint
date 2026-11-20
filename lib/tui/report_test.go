// report_test.go — the report TUI model tests. Structural assertions (no render
// snapshots — the convention): the diagnostic card count, titles, detail
// expansion, metrics-view switching, and quit keys. The fixtures are synthetic demo data (the storage principle).
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/ops"
)

// diagFixture — a synthetic ReportOutput with 2 diagnostic items (1 warn, 1
// info) and minimal metrics. The diagnostic sentence values only check that the TUI exposes the source as-is.
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

// updateKey — a helper that feeds 1 key input to the model and returns it as a ReportModel.
func updateKey(t *testing.T, m ReportModel, key tea.KeyType) ReportModel {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: key})
	mm, ok := next.(ReportModel)
	if !ok {
		t.Fatalf("Update returned %T, want ReportModel", next)
	}
	if cmd != nil {
		_ = cmd() // whether it is tea.Quit is asserted separately
	}
	return mm
}

// TestReportModelCardList — the diagnostic item count and card titles rendered:
// with 2 items, both signal titles are visible (the diagnostic view default).
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

// TestReportModelDetailExpand — expanding the selected item (enter) shows the
// Basis, Action, and tuning-key fields along with Value (contract: selection detail).
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

// TestReportModelMetricsView — tab switches to the metrics summary view: the window
// totals, recalls, key ratios, and counts are visible (contract: metrics summary navigation).
func TestReportModelMetricsView(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	m = updateKey(t, m, tea.KeyTab)
	v := m.View()
	for _, want := range []string{
		"Metrics",
		"writes 5",  // the Days sum
		"reads 18",  // the Days sum
		"hit 90%",   // effect.hit_pct
		"pending 2", // counts.pending
	} {
		if !strings.Contains(v, want) {
			t.Errorf("metrics view missing %q", want)
		}
	}
}

// TestReportModelEmptyDiagnosis — 0 diagnostics gets the normal-silence message
// (the metrics view remains navigable — no empty screen).
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

// TestReportModelCursorMoveAndDetail — after moving the cursor (j/k), expansion
// shows the detail of the item the cursor points at (the second item).
func TestReportModelCursorMoveAndDetail(t *testing.T) {
	m := NewReportModel(diagFixture(), 7)
	m = updateKey(t, m, tea.KeyDown)
	m = updateKey(t, m, tea.KeyEnter)
	v := m.View()
	if !strings.Contains(v, "diag_hot_budget_low=0.2") {
		t.Errorf("detail should show second item basis after cursor down: %q", v)
	}
}

// TestReportModelQuitKeys — the q and esc quit conventions.
func TestReportModelQuitKeys(t *testing.T) {
	// tea.Quit is a Cmd (a func), so it cannot be compared directly — judged by a
	// type assertion on the executed product tea.QuitMsg.
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
	// the 'q' rune key also quits.
	_, cmd := NewReportModel(diagFixture(), 7).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("rune 'q' should quit")
	}
}

// updateSize — a resize simulation helper (the report model — width stored).
func updateSize(m ReportModel, w int) ReportModel {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
	mm, ok := next.(ReportModel)
	if !ok {
		panic("Update returned non-ReportModel")
	}
	return mm
}

// RF-narrow — contract 1: 40 columns — both modes keep the key-hint line within
// the width while keeping the "q quit" cue, and the exit guidance (nonInteractiveHint) is wrapped.
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

// TestSeverityOf — the severity-derivation mapping: the sole 'informational'
// item (hot_budget_slack — informational per the diagnostic wording contract)
// is info; the rest are warn. DiagnosisItem has no severity field, so the TUI derives it from the signal id (see the assumption log).
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
