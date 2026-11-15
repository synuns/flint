// export_test.go — v1.5.0 T8: `flint export --tui` 흐름 계약 테스트.
// huh 폼·확인 단계·터미널 판정은 seam var로 fake 주입 — 검증 대상은
// ① 폼 결과→플래그 인자 조립(ExportArgs 단일 진실원) ② 미리보기 행 수·
// 바이트 산출 위임 ③ 상한 초과 오류 ④ 실행 위임(RunExport 재사용 —
// 중복 구현 금지). 실폼 연결은 seam 기본값(컴파일 시점) 활성화.
package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// overrideExportTUISeams — export TUI seam(터미널 판정·폼·확인) 일괄 교체.
// form·confirm이 nil이면 기본 seam을 그대로 둔다(터미널 판정만 교체).
func overrideExportTUISeams(t *testing.T, term bool, form func(*ExportSelection) error, confirm func(ExportPreview) (bool, error)) {
	t.Helper()
	oldTerm, oldForm, oldConfirm := isTerminal, exportFormFn, exportConfirmFn
	isTerminal = func() bool { return term }
	if form != nil {
		exportFormFn = form
	}
	if confirm != nil {
		exportConfirmFn = confirm
	}
	t.Cleanup(func() { isTerminal, exportFormFn, exportConfirmFn = oldTerm, oldForm, oldConfirm })
}

// RF1 — 폼 결과→인자 조립: format·status·out 순서로 --format/--status/
// --out 조립. Out이 빈 문자열(stdout)이면 --out 생략.
func TestExportArgsAssemblesFormSelection(t *testing.T) {
	got := ExportArgs(ExportSelection{Format: "md", Status: "all", Out: "/tmp/export.md"})
	want := []string{"--format", "md", "--status", "all", "--out", "/tmp/export.md"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
	// stdout 모드(Out 없음): --out 미포함.
	got = ExportArgs(ExportSelection{Format: "jsonl", Status: "active"})
	for _, a := range got {
		if a == "--out" {
			t.Fatalf("empty Out must omit --out: %v", got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("args = %v, want 4 elements [--format f --status s]", got)
	}
}

// RF2 — 폼 선택이 미리보기·실행으로 동일하게 전달: Preview는 선택된
// 필터로 행 수·바이트를 반환하고, Run은 조립된 인자로 기존 플래그
// 경로(runExport)를 재실행한다(exit 코드 통과).
func TestRunExportFormSelectionFlowsToPreviewAndRun(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error {
			sel.Format, sel.Status, sel.Out = "csv", "all", "/tmp/demo.csv"
			return nil
		},
		func(pv ExportPreview) (bool, error) { return true, nil })
	var gotSel ExportSelection
	var gotArgs []string
	ran := 0
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			gotSel = sel
			return ExportPreview{Rows: 3, Bytes: 120}, nil
		},
		MaxRows: func(ctx context.Context) int { return 100 },
		Run: func(args []string) int {
			ran++
			gotArgs = args
			return 0
		},
	}
	code, err := RunExport(context.Background(), deps)
	if err != nil || code != 0 {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if ran != 1 {
		t.Fatalf("Run called %d times, want 1", ran)
	}
	if gotSel.Format != "csv" || gotSel.Status != "all" || gotSel.Out != "/tmp/demo.csv" {
		t.Fatalf("selection not propagated to preview: %+v", gotSel)
	}
	want := []string{"--format", "csv", "--status", "all", "--out", "/tmp/demo.csv"}
	if len(gotArgs) != len(want) {
		t.Fatalf("Run args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("Run args = %v, want %v", gotArgs, want)
		}
	}
}

// RF3 — 비터미널(파이프·CI): 폼 시작 전 즉시 폴백 (3, 오류) — T5 관례
// "TUI requires a terminal". 폼은 열리지 않는다.
func TestRunExportRequiresTerminal(t *testing.T) {
	overrideExportTUISeams(t, false,
		func(sel *ExportSelection) error { t.Fatal("form must not open without a terminal"); return nil },
		nil)
	deps := ExportDeps{Run: func(args []string) int { t.Fatal("Run must not execute"); return 1 }}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
	if !strings.Contains(err.Error(), "TUI requires a terminal") {
		t.Fatalf("error must name the terminal requirement: %v", err)
	}
}

// RF4 — 사용자 중단(ctrl+c, huh.ErrUserAborted): "export cancelled" 안내
// 후 실행 없이 (0, nil) — 깨끗한 취소는 오류가 아니다(안내는 RF4a).
func TestRunExportUserAbortExitsCleanly(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { return huh.ErrUserAborted },
		nil)
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			t.Fatal("preview must not run after abort")
			return ExportPreview{}, nil
		},
		Run: func(args []string) int { t.Fatal("Run must not execute after abort"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
}

// RF4a — A3: 폼 중단·확인 거절 모두 조용한 0바이트가 아니라
// "export cancelled" 1행 안내(CancelNote — 기본 os.Stdout) 후 exit 0.
func TestRunExportCancelPrintsNotice(t *testing.T) {
	t.Run("form abort", func(t *testing.T) {
		var out bytes.Buffer
		overrideExportTUISeams(t, true,
			func(sel *ExportSelection) error { return huh.ErrUserAborted },
			nil)
		deps := ExportDeps{
			CancelNote: &out,
			Run:        func(args []string) int { t.Fatal("Run must not execute"); return 1 },
		}
		code, err := RunExport(context.Background(), deps)
		if code != 0 || err != nil {
			t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
		}
		if !strings.Contains(out.String(), "export cancelled") {
			t.Fatalf("stdout = %q, want 'export cancelled' notice", out.String())
		}
	})
	t.Run("confirm declined", func(t *testing.T) {
		var out bytes.Buffer
		overrideExportTUISeams(t, true,
			func(sel *ExportSelection) error { sel.Format, sel.Status = "md", "active"; return nil },
			func(pv ExportPreview) (bool, error) { return false, nil })
		deps := ExportDeps{
			CancelNote: &out,
			Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
				return ExportPreview{Rows: 1, Bytes: 10}, nil
			},
			MaxRows: func(ctx context.Context) int { return 100 },
			Run:     func(args []string) int { t.Fatal("Run must not execute when declined"); return 1 },
		}
		code, err := RunExport(context.Background(), deps)
		if code != 0 || err != nil {
			t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
		}
		if !strings.Contains(out.String(), "export cancelled") {
			t.Fatalf("stdout = %q, want 'export cancelled' notice", out.String())
		}
	})
}

// RF5 — 상한 초과 시 TUI 오류: 미리보기 행 수 > export_import_max_rows면
// 확인 단계 대신 오류로 (3, err) — 실행(Run)은 호출되지 않는다.
func TestRunExportOverLimitShowsTUIError(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "jsonl", "all"; return nil },
		func(pv ExportPreview) (bool, error) {
			t.Fatal("confirm must not run over the limit")
			return false, nil
		})
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{Rows: 3, Bytes: 90}, nil
		},
		MaxRows: func(ctx context.Context) int { return 2 },
		Run:     func(args []string) int { t.Fatal("Run must not execute over the limit"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "max rows") || !strings.Contains(msg, "export_import_max_rows") {
		t.Fatalf("error must name the limit and its key: %v", msg)
	}
}

// RF6 — 미리보기 확인 거절: 실행 없이 (0, nil).
func TestRunExportDeclineSkipsRun(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "md", "active"; return nil },
		func(pv ExportPreview) (bool, error) { return false, nil })
	var previewed ExportPreview
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			previewed = ExportPreview{Rows: 2, Bytes: 64}
			return previewed, nil
		},
		MaxRows: func(ctx context.Context) int { return 100 },
		Run:     func(args []string) int { t.Fatal("Run must not execute when declined"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if previewed.Rows != 2 {
		t.Fatalf("preview must run before confirm: %+v", previewed)
	}
}

// RF7 — 미리보기 조회 실패: (3, err), 실행 없음.
func TestRunExportPreviewFailureExits3(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { return nil },
		nil)
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{}, context.DeadlineExceeded
		},
		Run: func(args []string) int { t.Fatal("Run must not execute on preview failure"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
}

// RF8 — 경계(행 수 == max): 초과 아님 — 확인 단계로 진행.
func TestRunExportBoundaryEqualMaxProceeds(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "jsonl", "active"; return nil },
		func(pv ExportPreview) (bool, error) { return true, nil })
	ran := 0
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{Rows: 2, Bytes: 48}, nil
		},
		MaxRows: func(ctx context.Context) int { return 2 },
		Run: func(args []string) int {
			ran++
			return 0
		},
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if ran != 1 {
		t.Fatalf("boundary ==max must proceed to run, ran=%d", ran)
	}
}
