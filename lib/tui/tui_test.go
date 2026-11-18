// tui_test.go — TUI 기반 래퍼 테스트: 비터미널 폴백 단언.
package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/ops"
)

// stubModel — 래퍼 테스트용 최소 모델(Run이 시작 전에 반환하므로 미사용).
type stubModel struct{}

func (stubModel) Init() tea.Cmd                         { return nil }
func (m stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (stubModel) View() string                          { return "" }

// TestRunRejectsNonTerminal — go test 표준 출력은 파이프이므로
// Run은 시작 전 즉시 오류를 반환해야 한다(호출부 exit 3 폴백).
func TestRunRejectsNonTerminal(t *testing.T) {
	err := Run(stubModel{})
	if err == nil {
		t.Fatal("Run(non-terminal stdout) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "TUI requires a terminal") {
		t.Errorf("error message %q, want it to mention TUI requires a terminal", err.Error())
	}
	if !strings.Contains(err.Error(), "flag-mode") {
		t.Errorf("error message %q, want it to mention flag-mode fallback", err.Error())
	}
}

// TestRunRejectsForcedNonTerminal — 판정 함수를 주입해 비터미널 경로 재확인.
func TestRunRejectsForcedNonTerminal(t *testing.T) {
	orig := isTerminal
	isTerminal = func() bool { return false }
	defer func() { isTerminal = orig }()

	if err := Run(stubModel{}); err == nil {
		t.Fatal("Run(forced non-terminal) = nil error, want error")
	}
}

// TestIsTerminalStdoutDelegates — 공개 판정 래퍼는 isTerminal
// seam을 그대로 통과한다(진입 분기 seam의 기본 실구현).
func TestIsTerminalStdoutDelegates(t *testing.T) {
	orig := isTerminal
	isTerminal = func() bool { return true }
	defer func() { isTerminal = orig }()
	if !IsTerminalStdout() {
		t.Error("IsTerminalStdout() = false, want true (seam 통과)")
	}
	isTerminal = func() bool { return false }
	if IsTerminalStdout() {
		t.Error("IsTerminalStdout() = true, want false (seam 통과)")
	}
}

// TestNonInteractiveHintInViews — pending·report 화면 하단에
// 비대화 탈출 안내 1줄이 렌더된다(export는 huh 폼 설명으로 제공).
func TestNonInteractiveHintInViews(t *testing.T) {
	pm := NewPendingModel(context.TODO(), nil, nil, PendingActions{})
	if v := pm.View(); !strings.Contains(v, nonInteractiveHint) {
		t.Errorf("pending view missing non-interactive hint:\n%s", v)
	}
	rm := NewReportModel(ops.ReportOutput{}, 7)
	if v := rm.View(); !strings.Contains(v, nonInteractiveHint) {
		t.Errorf("report view missing non-interactive hint:\n%s", v)
	}
}

// TestIsTerminalDevNullNotTerminal — /dev/null은
// char device이므로 구 ModeCharDevice 판정이 이를 터미널로 오탐했다
// (리다이렉션 stdout > /dev/null에서 TUI 폴백 exit 3). isatty 계열
// 판정(charmbracelet/x/term)은 /dev/null을 거짓으로 판별해야 한다.
func TestIsTerminalDevNullNotTerminal(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/null 열기 실패: %v", err)
	}
	orig := os.Stdout
	os.Stdout = devNull
	defer func() {
		os.Stdout = orig
		devNull.Close()
	}()

	if isTerminal() {
		t.Errorf("isTerminal() = true for %s (char device), want false — /dev/null 리다이렉션 오탐", os.DevNull)
	}
}

// TestIsTerminalStdoutEnvEscapeHatch — FLINT_NO_TUI 설정 시
// (값 무관) 환경 체크가 우선한다 — 터미널이어도 판정 거짓(적응형 TUI
// 진입 전면 차단, 현행 경로 강제). GH_PROMPT_DISABLED류 표준 패턴.
func TestIsTerminalStdoutEnvEscapeHatch(t *testing.T) {
	orig := isTerminal
	isTerminal = func() bool { return true } // 강제 터미널
	defer func() { isTerminal = orig }()

	t.Setenv("FLINT_NO_TUI", "1")
	if !NoTUIForced() {
		t.Error("NoTUIForced() = false, want true (설정 시 값 무관)")
	}
	if IsTerminalStdout() {
		t.Error("IsTerminalStdout() = true under FLINT_NO_TUI, want false (환경 체크 우선)")
	}

	t.Setenv("FLINT_NO_TUI", "") // 빈 값은 미설정 취급(os.Getenv != "" 규약)
	if NoTUIForced() || !IsTerminalStdout() {
		t.Error("빈 값은 미차단이어야 함(os.Getenv != \"\" 규약)")
	}

	os.Unsetenv("FLINT_NO_TUI")
	if NoTUIForced() {
		t.Error("NoTUIForced() = true unset, want false")
	}
	if !IsTerminalStdout() {
		t.Error("IsTerminalStdout() = false (터미널 + 미설정), want true")
	}
}
