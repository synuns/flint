// tui_test.go — the TUI-base wrapper tests: non-terminal fallback assertions.
package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/ops"
)

// stubModel — a minimal model for the wrapper tests (unused since Run returns before starting).
type stubModel struct{}

func (stubModel) Init() tea.Cmd                         { return nil }
func (m stubModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (stubModel) View() string                          { return "" }

// TestRunRejectsNonTerminal — go test's stdout is a pipe, so Run must return
// an error immediately before starting (the caller falls back to exit 3).
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

// TestRunRejectsForcedNonTerminal — re-verifies the non-terminal path by injecting the check function.
func TestRunRejectsForcedNonTerminal(t *testing.T) {
	orig := isTerminal
	isTerminal = func() bool { return false }
	defer func() { isTerminal = orig }()

	if err := Run(stubModel{}); err == nil {
		t.Fatal("Run(forced non-terminal) = nil error, want error")
	}
}

// TestIsTerminalStdoutDelegates — the public check wrapper passes through the
// isTerminal seam as-is (the entry-branch seam's default real implementation).
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

// TestNonInteractiveHintInViews — a 1-line non-interactive escape notice is
// rendered at the bottom of the pending and report screens (for export, provided in the huh form description).
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

// TestIsTerminalDevNullNotTerminal — /dev/null is also a char device, so the
// old ModeCharDevice check misdetected it as a terminal (a stdout > /dev/null
// redirection triggered the TUI fallback exit 3). The isatty-family check
// (charmbracelet/x/term) must judge /dev/null false.
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

// TestIsTerminalStdoutEnvEscapeHatch — with FLINT_NO_TUI set (any value), the
// environment check takes precedence — false even on a terminal (adaptive TUI
// entry blocked entirely, forcing the current path). The standard pattern of the GH_PROMPT_DISABLED kind.
func TestIsTerminalStdoutEnvEscapeHatch(t *testing.T) {
	orig := isTerminal
	isTerminal = func() bool { return true } // forced terminal
	defer func() { isTerminal = orig }()

	t.Setenv("FLINT_NO_TUI", "1")
	if !NoTUIForced() {
		t.Error("NoTUIForced() = false, want true (설정 시 값 무관)")
	}
	if IsTerminalStdout() {
		t.Error("IsTerminalStdout() = true under FLINT_NO_TUI, want false (환경 체크 우선)")
	}

	t.Setenv("FLINT_NO_TUI", "") // the empty value counts as unset (the os.Getenv != "" convention)
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
