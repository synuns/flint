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

// noTUIEnvVar — v1.5.2 T1: TUI 진입 차단 환경 변수(GH_PROMPT_DISABLED류
// 표준 탈출구 패턴). 비어 있지 않은 값으로 설정 시("1" 권장 — 빈 값은
// 미설정 취급, os.Getenv != "" 규약) 적응형 TUI 진입을 전면 차단하고
// 현행 경로를 강제한다. 에이전트·CI가 명시적으로 비대화를 지정하는
// 수단 — 3층 방어(환경변수 → isTerminal → 플래그 감지)의 최상위 1층.
const noTUIEnvVar = "FLINT_NO_TUI"

// NoTUIForced — FLINT_NO_TUI 설정 여부(비어 있지 않은 값). 적응형 진입
// 분기는 이 판정을 TTY seam보다 먼저 상시한다(각 cli 진입 분기 참조).
func NoTUIForced() bool {
	return os.Getenv(noTUIEnvVar) != ""
}

// IsTerminalStdout — v1.5.2 T1: 표준 출력 터미널 판정 공개 래퍼. 적응형
// 진입 분기(pending·export·report 무인자 + TTY → TUI)의 판정 원천 —
// cli 커맨드는 이 함수를 주입 가능 seam var로 감싸 테스트한다.
// 환경 체크 우선: FLINT_NO_TUI 설정 시 터미널이어도 거짓.
func IsTerminalStdout() bool { return !NoTUIForced() && isTerminal() }

// nonInteractiveHint — v1.5.2 T1 적응형 전환 안내: 비대화 탈출 방법 1줄.
// 각 TUI 화면 하단(export는 huh 폼 설명)에 표기된다 — 무인자 실행이
// 이제 TUI로 흐르므로 파이프·플래그 경로가 그대로 있음을 알린다.
const nonInteractiveHint = "pipe output or pass flags for non-interactive mode"

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
