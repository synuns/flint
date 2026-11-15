// Package tui — flint 대화형 TUI 기반(bubbletea+huh) 공통 래퍼.
// v1.5.0 T5: 화면 모델(T6~T9)이 이 래퍼 위에 올라간다.
package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// isTerminal — 표준 출력이 대화형 터미널인지 판정(테스트 주입 가능).
// 파이프·리다이렉션·CI 환경에서 거짓.
var isTerminal = func() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Run — bubbletea 프로그램 실행 래퍼. 표준 출력이 터미널이 아니면
// 프로그램 시작 전 즉시 오류를 반환한다 — 호출부는 이 오류를 받아
// exit 3으로 폴백(플래그 모드 안내)한다. ESC/q 종료 관례는 각 화면
// 모델이 따른다.
func Run(model tea.Model) error {
	if !isTerminal() {
		return fmt.Errorf("TUI requires a terminal — use flag-mode instead")
	}
	p := tea.NewProgram(model)
	_, err := p.Run()
	return err
}
