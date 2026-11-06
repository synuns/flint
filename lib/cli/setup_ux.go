// setupUX — setup 진행 표시(v0.17.0 R2). stdlib fmt+ANSI 이스케이프만
// 사용(ADR-0003 외부 의존성 금지). TTY면 스텝 인디케이터(◯… → ✓/✗)와
// 색상으로, non-TTY면 색상 코드 없는 plain 한 줄 결과로 렌더한다.
package cli

import (
	"fmt"
	"io"
	"os"
)

// ANSI 색상 상수 — ✓ 그린 · ✗ 레드 · 세부 딤 · 제목 볼드.
const (
	ansiGreen = "\033[32m✓\033[0m"
	ansiRed   = "\033[31m✗\033[0m"
	ansiDim   = "\033[2m%s\033[0m"
	ansiBold  = "\033[1m%s\033[0m"
)

// setupUX — 진행 표시기. lastStep은 Step으로 지정한 최근 스텝 이름으로,
// Success/Fail이 그 줄을 \r로 덮어 완성한다.
type setupUX struct {
	w        io.Writer
	isTTY    bool
	lastStep string
}

// newSetupUX — w로 출력한다. forceColor가 true면 TTY로 취급(테스트 주입),
// 아니면 *os.File의 Stat(mode & ModeCharDevice)으로 감지한다. 버퍼·파이프
// 등 *os.File이 아닌 writer는 non-TTY다.
func newSetupUX(w io.Writer, forceColor bool) *setupUX {
	isTTY := forceColor
	if !isTTY {
		if f, ok := w.(*os.File); ok {
			if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
				isTTY = true
			}
		}
	}
	return &setupUX{w: w, isTTY: isTTY}
}

// Step — 진행 중 스텝 표시("  ◯ name…"). TTY는 줄바꿈 없이 찍어 완료
// 표시가 같은 줄을 덮어쓰게 하고, non-TTY는 결과 한 줄만 남기도록 찍지 않는다.
func (u *setupUX) Step(name string) {
	u.lastStep = name
	if u.isTTY {
		fmt.Fprintf(u.w, "  ◯ %s…", name)
	}
}

// Success — 스텝 성공. TTY: "\r  ✓ name — detail"(그린+딤 세부),
// non-TTY: "name: ok (detail)".
func (u *setupUX) Success(detail string) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiGreen, u.lastStep, fmt.Sprintf(ansiDim, detail))
		return
	}
	fmt.Fprintf(u.w, "%s: ok (%s)\n", u.lastStep, detail)
}

// Fail — 스텝 실패. TTY: "\r  ✗ name — err"(레드), non-TTY: "name: failed (err)".
func (u *setupUX) Fail(err error) {
	if u.isTTY {
		fmt.Fprintf(u.w, "\r  %s %s — %s\n", ansiRed, u.lastStep, err)
		return
	}
	fmt.Fprintf(u.w, "%s: failed (%v)\n", u.lastStep, err)
}

// Summary — 완료 후 다음 단계 안내(③seed ④플러그인 — 실행이 아닌 안내).
func (u *setupUX) Summary(next []string) {
	if len(next) == 0 {
		return
	}
	if u.isTTY {
		fmt.Fprintf(u.w, "\n"+ansiBold+"\n", "다음 단계")
	} else {
		fmt.Fprintln(u.w, "다음 단계:")
	}
	for i, n := range next {
		fmt.Fprintf(u.w, "  %d. %s\n", i+1, n)
	}
}
