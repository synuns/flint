// report_text_diag_test.go — the report --text diagnostics-wiring regression.
// The text leg of the all-three-views-one-source contract (the diagnosis.go comment —
// text (Sentence), the default JSON field, and --html cards): --text prints
// ReportOutput.Diagnosis as Sentence() lines, and with 0 diagnostics it emits no section
// (normal silence). The default (no flags) one-line JSON and the --html path are unchanged (output-contract defense).
package ops

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// diagFake — a fixture triggering 2 diagnostics (tier B: pending backlog + a dead warning).
// The other queries are blank (tallies 0, no metering — the normal-silence principle).
func diagFake() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "status = 'pending'"): // the counts 3 columns
			return [][]any{{int64(3), int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* stale */"):
			return [][]any{{int64(0), int64(0)}}, nil
		case strings.Contains(sql, "invalid_at >= $1"): // period invalidations
			return [][]any{{int64(0)}}, nil
		case strings.Contains(sql, "count(CASE WHEN read_count > 0"): // the G3 denominator
			return [][]any{{int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* dead */"): // 5 active rows with 0 recalls
			return [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}, nil
		}
		return nil, nil
	}}
}

// TestTextReportPrintsDiagnosis — --text: the 2 Diagnose products (pending_backlog
// and dead_knowledge) each print as a Sentence() line on stdout.
func TestTextReportPrintsDiagnosis(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), diagFake(), []string{"--text"}, nil, &out); code != 0 {
		t.Fatalf("report --text exit=%d: %s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"pending_backlog: 3 pending items awaiting approval (basis: inherits doctor pending-age pending_age_days=7d (age threshold)) → review via approve or clean up with maintain --expire-pending",
		"dead_knowledge: dead_pct 100% (active but never recalled, long elapsed) (basis: diag_dead_warn=10% (dead_pct at dead_days=30d)) → consider cleanup with maintain --stale and --prune-invalidated",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--text 출력에 Sentence 문장 부재:\nwant %q\ngot  %s", want, got)
		}
	}
}

// TestTextReportSilentWhenNoDiagnosis — --text + 0 diagnostics: no diagnostics
// section (normal silence — not even a header).
func TestTextReportSilentWhenNoDiagnosis(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), &fakeStore{}, []string{"--text"}, nil, &out); code != 0 {
		t.Fatalf("report --text exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "진단") {
		t.Errorf("진단 0건인데 진단 섹션 출력됨: %s", out.String())
	}
}

// TestDefaultOutputStaysJSON — no flags: the one-line JSON unchanged (diagnosis
// exists as a JSON field, no text sentence render — 1 line).
func TestDefaultOutputStaysJSON(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), diagFake(), nil, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	s := strings.TrimRight(out.String(), "\n")
	if n := strings.Count(s, "\n"); n > 0 {
		t.Errorf("기본 출력이 한 줄이 아님(%d개 개행): %q", n, s)
	}
	var rep ReportOutput
	if err := json.Unmarshal([]byte(s), &rep); err != nil {
		t.Fatalf("기본 출력 JSON 파싱 실패: %v: %s", err, s)
	}
	if len(rep.Diagnosis) != 2 {
		t.Errorf("기본 JSON diagnosis=%d건, want 2(JSON 필드는 불변)", len(rep.Diagnosis))
	}
}

// TestHTMLOutputUnchanged — --html: the existing path unchanged (a one-line stdout
// summary + the diagnostic signals rendered in the file's cards — not passing through the new --text code).
func TestHTMLOutputUnchanged(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate the default report.html output
	var out strings.Builder
	if code := RunReport(context.Background(), diagFake(), []string{"--html"}, nil, &out); code != 0 {
		t.Fatalf("report --html exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Errorf("--html stdout 요약 불변 아님: %s", out.String())
	}
	html, err := os.ReadFile("report.html")
	if err != nil {
		t.Fatalf("report.html 생성 안 됨: %v", err)
	}
	for _, want := range []string{"pending_backlog", "dead_knowledge"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("HTML 진단 카드에 %s 부재", want)
		}
	}
}
