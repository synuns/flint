// Package tui — flint 대화형 TUI 기반(bubbletea+huh) 공통 래퍼.
// 화면 모델이 이 래퍼 위에 올라간다.
package tui

import (
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/synuns/flint/lib/wire"
)

// isTerminal — 표준 출력이 대화형 터미널인지 판정(테스트 주입 가능).
// 파이프·리다이렉션·CI 환경에서 거짓. 구 ModeCharDevice
// 휴리스틱은 /dev/null도 char device라 터미널로 오탐했다(stdout >
// /dev/null 리다이렉션에서 TUI 폴백 exit 3). isatty ioctl 계열인
// term.IsTerminal(fd)로 교체해 char device 중 터미널만 판정한다.
var isTerminal = func() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

// noTUIEnvVar — TUI 진입 차단 환경 변수(GH_PROMPT_DISABLED류
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

// IsTerminalStdout — 표준 출력 터미널 판정 공개 래퍼. 적응형
// 진입 분기(pending·export·report 무인자 + TTY → TUI)의 판정 원천 —
// cli 커맨드는 이 함수를 주입 가능 seam var로 감싸 테스트한다.
// 환경 체크 우선: FLINT_NO_TUI 설정 시 터미널이어도 거짓.
func IsTerminalStdout() bool { return !NoTUIForced() && isTerminal() }

// nonInteractiveHint — 적응형 전환 안내: 비대화 탈출 방법 1줄.
// 각 TUI 화면 하단(export는 huh 폼 설명)에 표기된다 — 무인자 실행이
// 이제 TUI로 흐르므로 파이프·플래그 경로가 그대로 있음을 알린다.
const nonInteractiveHint = "pipe output or pass flags for non-interactive mode"

// lowHeightThreshold — 저화면(크롬 최소화) 임계(공통 상수): 창 높이가
// 이 값 미만이면 화면은 선택 크롬(비대화 안내·구분 여백·status)을
// View에서 생략하고 목록 배분 공식이 줄어든 크롬 행수를 반영한다.
// q/ESC 탈출 안내는 저화면에서도 유지된다.
const lowHeightThreshold = 12

// lowHeight — 저화면(크롬 최소화) 분기 판정. 창 크기 미수신(0)은
// 리사이즈 전 초기 렌더 — 기존 레이아웃을 유지한다.
func lowHeight(h int) bool { return h > 0 && h < lowHeightThreshold }

// Run — bubbletea 프로그램 실행 래퍼. 표준 출력이 터미널이 아니면
// 프로그램 시작 전 즉시 오류를 반환한다 — 호출부는 이 오류를 받아
// exit 3으로 폴백(플래그 모드 안내)한다. ESC/q 종료 관례는 각 화면
// 모델이 따른다.
// ErrTerminalRequired — 비터미널에서의 TUI 강제(--tui)
// 는 무효 인자다 — 호출부 exit 3 불변, ErrLine 분류는 error:"usage"
// ("commands left without the usage type: 0" 계약의 잔여 사각 —
// error:"internal" 보고 폐지). sentinel이라 errors.Is 판별도 유지된다.
var ErrTerminalRequired = errors.New("TUI requires a terminal — use flag-mode instead")

// Run — bubbletea 프로그램 실행 래퍼. 표준 출력이 터미널이 아니면
// 프로그램 시작 전에 즉시 오류를 반환한다 — 호출부는 이 오류를 받아
// exit 3으로 폴백(플래그 모드 안내)한다. ESC/q 종료 관례는 각 화면
// 모델이 따른다.
func Run(model tea.Model) error {
	if !isTerminal() {
		return wire.UsageErrorf("%w", ErrTerminalRequired)
	}
	p := tea.NewProgram(model)
	_, err := p.Run()
	return err
}
