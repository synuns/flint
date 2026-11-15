// report_tui_test.go — v1.5.0 T9 report --tui 진입 분기 테스트.
// protocol: --tui 오틋인 전 기본 3뷰(JSON·text·HTML) 경로 불변 ·
// --tui 조합 오류 exit 3 · 비터미널 폴백 exit 3 · 수집 위임·오류 전파.
// 수집 producer(ops.RunReport)는 fake Store 경유 실구현(SUPERLITE_REAL=1
// 동일 경로), 화면 기동(reportTUIFn)은 stub seam.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
)

// runEntryArgs — 진입 래퍼(runReportEntry) 직접 실행 헬퍼.
func runEntryArgs(t *testing.T, f Store, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := runReportEntry(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// TestReportEntryFlagPathUnchanged — --tui 없이 들어오면 기존 JSON 경로
// 바이트 동일 위임(기본 경로 불변 — Review Focus protocol).
func TestReportEntryFlagPathUnchanged(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"days"`) {
		t.Errorf("default JSON path output changed: %q", out)
	}
}

// TestReportEntryTUIExclusive — --tui는 다른 뷰 플래그와 조합 불가
// (화면·파일 출력 이원 방지) — exit 3 에러 JSON.
func TestReportEntryTUIExclusive(t *testing.T) {
	for _, args := range [][]string{
		{"--tui", "--html"},
		{"--tui", "--text"},
		{"--tui", "--out", "x.html"},
	} {
		out, code := runEntryArgs(t, newReportFake(nil), args...)
		if code != 3 {
			t.Errorf("%v: exit=%d, want 3 (out %q)", args, code, out)
		}
		if !strings.Contains(out, `"error"`) || !strings.Contains(out, "--tui") {
			t.Errorf("%v: error JSON should name --tui conflict: %q", args, out)
		}
	}
}

// TestReportEntryTUIRunsScreen — --tui는 수집(실구현 — fake Store) 후
// 화면 모델에 ReportOutput·창 일수를 넘긴다. stdout에 JSON을 남기지
// 않는다(뷰 전용).
func TestReportEntryTUIRunsScreen(t *testing.T) {
	daily := map[string]string{}
	for _, d := range reportDates(3) {
		daily["daily:"+d] = `{"write":1,"read":2,"dup":0,"hot":1}`
	}
	f := newReportFake(daily)

	orig := reportTUIFn
	var gotRep ops.ReportOutput
	var gotDays int
	reportTUIFn = func(rep ops.ReportOutput, days int) error {
		gotRep, gotDays = rep, days
		return nil
	}
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, f, "--tui", "--days", "3")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(gotRep.Days) != 3 {
		t.Errorf("screen got %d day rows, want 3 (collection reuse)", len(gotRep.Days))
	}
	if gotDays != 3 {
		t.Errorf("screen got days=%d, want 3 (window from collected rows)", gotDays)
	}
	if strings.Contains(out, `"days"`) {
		t.Errorf("TUI path must not print JSON to stdout: %q", out)
	}
}

// TestReportEntryTUINonTerminalFallback — 비터미널(파이프·CI)에서 화면
// 기동 오류는 exit 3 안내 폴백(T5 계약 문구).
func TestReportEntryTUINonTerminalFallback(t *testing.T) {
	orig := reportTUIFn
	reportTUIFn = nil // 실구현 유지 관례: nil이면 기본값 사용
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, newReportFake(nil), "--tui")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "TUI requires a terminal") {
		t.Errorf("fallback should mention terminal requirement: %q", out)
	}
}

// TestReportEntryTUICollectErrorPropagates — 수집 단계 오류(서버다운)는
// 기존 JSON 경로와 동일 에러·exit로 전파(2).
func TestReportEntryTUICollectErrorPropagates(t *testing.T) {
	f := &fakeStore{pingErr: store.ErrServerDown}
	orig := reportTUIFn
	called := false
	reportTUIFn = func(ops.ReportOutput, int) error { called = true; return nil }
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, f, "--tui")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; out=%s", code, out)
	}
	if !strings.Contains(out, "server_down") {
		t.Errorf("collect error not propagated: %q", out)
	}
	if called {
		t.Error("screen must not launch when collection fails")
	}
}

// TestReportEntryTUIBadFlagStillValidated — --tui와 함께 쓴 무효 플래그는
// 기존 인자 검증 경로가 그대로 담당(exit 3).
func TestReportEntryTUIBadFlagStillValidated(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil), "--tui", "--days", "abc")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("invalid flag should surface error JSON: %q", out)
	}
}
