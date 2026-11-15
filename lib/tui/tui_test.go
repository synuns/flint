// tui_test.go — TUI 기반 래퍼 테스트: 비터미널 폴백 단언. v1.5.0 T5
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
