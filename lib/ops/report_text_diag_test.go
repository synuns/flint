package ops

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func diagFake() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "status = 'pending'"):
			return [][]any{{int64(3), int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* stale */"):
			return [][]any{{int64(0), int64(0)}}, nil
		case strings.Contains(sql, "invalid_at >= $1"):
			return [][]any{{int64(0)}}, nil
		case strings.Contains(sql, "count(CASE WHEN read_count > 0"):
			return [][]any{{int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* dead */"):
			return [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}, nil
		}
		return nil, nil
	}}
}

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

func TestTextReportSilentWhenNoDiagnosis(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), &fakeStore{}, []string{"--text"}, nil, &out); code != 0 {
		t.Fatalf("report --text exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "진단") {
		t.Errorf("진단 0건인데 진단 섹션 출력됨: %s", out.String())
	}
}

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

func TestHTMLOutputUnchanged(t *testing.T) {
	t.Chdir(t.TempDir())
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
