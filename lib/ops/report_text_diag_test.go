// report_text_diag_test.go — v1.2.3 R17 C3: report --text 진단 배선 회귀.
// 세 뷰 동일 원천 계약(diagnosis.go 주석 — text(Sentence)·기본 JSON 필드·
// --html 카드)의 text 담당: --text에서 ReportOutput.Diagnosis를 Sentence()
// 라인으로 출력하고, 진단 0건이면 섹션을 내지 않는다(정상 침묵).
// 기본(플래그 없음) 한 줄 JSON·--html 경로는 불변(출력 계약 방어).
package ops

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// diagFake — 진단 2건 트리거 픽스처(B계층: pending 적체 + dead 경고).
// 나머지 질의는 공란(집계 0·계측 없음 — 정상 침묵 원칙).
func diagFake() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "status = 'pending'"): // counts 3열
			return [][]any{{int64(3), int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* stale */"):
			return [][]any{{int64(0), int64(0)}}, nil
		case strings.Contains(sql, "invalid_at >= $1"): // 기간 무효화
			return [][]any{{int64(0)}}, nil
		case strings.Contains(sql, "count(CASE WHEN read_count > 0"): // G3 분모
			return [][]any{{int64(5), int64(0)}}, nil
		case strings.Contains(sql, "/* dead */"): // 회상 0 활성 5행
			return [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}, nil
		}
		return nil, nil
	}}
}

// TestTextReportPrintsDiagnosis — --text: Diagnose 산물 2건(pending_backlog·
// dead_knowledge)이 각각 Sentence() 문장으로 stdout에 찍힌다.
func TestTextReportPrintsDiagnosis(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), diagFake(), []string{"--text"}, nil, &out); code != 0 {
		t.Fatalf("report --text exit=%d: %s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"pending_backlog: 승인 대기 pending 3건 (기준: doctor pending-age 승계 pending_age_days=7일(경과 기준일)) → approve 검수·maintain --expire-pending 정리",
		"dead_knowledge: dead_pct 100%(활성 중 회상 0·장기 경과) (기준: diag_dead_warn=10%(dead_days=30일 기준 dead_pct)) → maintain --stale·--prune-invalidated 정리 검토",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--text 출력에 Sentence 문장 부재:\nwant %q\ngot  %s", want, got)
		}
	}
}

// TestTextReportSilentWhenNoDiagnosis — --text + 진단 0건: 진단 섹션
// 미출력(정상 침묵 — 헤더조차 없음).
func TestTextReportSilentWhenNoDiagnosis(t *testing.T) {
	var out strings.Builder
	if code := RunReport(context.Background(), &fakeStore{}, []string{"--text"}, nil, &out); code != 0 {
		t.Fatalf("report --text exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "진단") {
		t.Errorf("진단 0건인데 진단 섹션 출력됨: %s", out.String())
	}
}

// TestDefaultOutputStaysJSON — 플래그 없음: 한 줄 JSON 불변(diagnosis는
// JSON 필드로 존재, text 문장 렌더 없음 — 라인 1개).
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

// TestHTMLOutputUnchanged — --html: 기존 경로 불변(stdout 한 줄 요약 +
// 파일 카드에 진단 신호 렌더 — --text 신규 코드 미경유).
func TestHTMLOutputUnchanged(t *testing.T) {
	t.Chdir(t.TempDir()) // report.html 기본 출력 격리
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
