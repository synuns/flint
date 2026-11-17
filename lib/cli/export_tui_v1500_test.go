// export_tui_v1500_test.go — `flint export --tui` 진입 분기 계약.
// runExport 본체(플래그 파싱·출력·사이드카·상한)는 기존 export_test.go가
// 소유한다 — 여기는 ① --tui 진입 분기가 TUI 흐름 seam으로 위임 ② --tui
// 미지정 시 플래그 경로 불변 ③ 기본 배선의 비터미널 폴백만 검증한다.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/tui"
)

// RF1 — 진입 분기: `flint export --tui`는 runExport 플래그 파싱 대신
// exportTUIFn(tui.RunExport)으로 위임하고 exit 코드를 통과시킨다.
func TestRunExportTUIEntryBranch(t *testing.T) {
	installExportFakes(t, nil)
	called := 0
	old := exportTUIFn
	exportTUIFn = func(ctx context.Context, deps tui.ExportDeps) (int, error) {
		called++
		if deps.Run == nil {
			t.Fatal("deps.Run must be wired to the flag path")
		}
		return 7, nil
	}
	t.Cleanup(func() { exportTUIFn = old })
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--tui"}, strings.NewReader(""), &stdout)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (TUI 위임 코드 통과)", code)
	}
	if called != 1 {
		t.Fatalf("exportTUIFn called %d times, want 1", called)
	}
}

// RF2 — 플래그 경로 회귀: --tui 미지정 시 기존 동작 불변 — TUI seam은
// 호출되지 않고 jsonl stdout·요약 stderr 계약이 그대로 유지된다.
func TestRunExportFlagPathUnchangedWithoutTUI(t *testing.T) {
	stderr := installExportFakes(t, nil)
	old := exportTUIFn
	exportTUIFn = func(ctx context.Context, deps tui.ExportDeps) (int, error) {
		t.Fatal("TUI flow must not run without --tui")
		return 1, nil
	}
	t.Cleanup(func() { exportTUIFn = old })
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "md"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<md of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (md, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF3 — 기본 배선 비터미널 폴백: 파이프·테스트 환경(터미널 아님)에서
// --tui는 exit 3 + "TUI requires a terminal" 오류 JSON 한 줄.
func TestRunExportTUIDefaultWiringNonTerminalFallback(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--tui"}, strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout %q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Fatalf("fallback message missing: %q", stdout.String())
	}
}
