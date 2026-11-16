// pending.go — v1.5.0 T7: `flint pending` 진입 분기. --tui 옵트인과
// v1.5.2 T1 적응형 전환(무인자 + 터미널)에서만 TUI(tui.RunPendingTUI)로,
// 그 외(플래그 존재·무인자 파이프·unknown flag exit 3)는
// knowledge.RunPending에 불변 위임한다(파이프·CI 무영향). TUI 런처·TTY
// 판정은 seam var(테스트 fake 주입).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// pendingTuiFn — TUI 진입 seam. 기본 실구현, 테스트는 fake 교체.
var pendingTuiFn = tui.RunPendingTUI

// pendingTTYFn — v1.5.2 T1 적응형 진입 TTY 판정 seam(setupWizardTTYFn
// 관례 — 기본 실구현 tui.IsTerminalStdout, 테스트 fake 교체).
var pendingTTYFn = tui.IsTerminalStdout

func init() {
	// knowledge.init 등록(RunPending)을 덮어쓴다 — wire.Register 최신값
	// 승계(같은 이름 재등록 규약). 기본 경로는 아래 위임으로 불변.
	Register("pending", runPendingEntry)
}

// runPendingEntry — pending 핸들러. 진입 규칙(v1.5.2 T1 통일):
// ① --tui 명시 → TUI(단독만 — 조합은 무효 인자 exit 3, 비터미널은
// TUI 런처 폴백 exit 3 불변) ② 인자·플래그 전혀 없음 + 터미널 → TUI
// ③ 무인자 + 비터미널(파이프) → RunPending 현행 출력 ④ 플래그 존재 →
// RunPending 현행 경로.
func runPendingEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
					"unknown flag: %s — usage: flint pending [--tui]", b)))
				return 3
			}
		}
		return pendingTuiFn(ctx, st, stdout)
	}
	// v1.5.2 T1 적응형 전환: 무인자 + 터미널이면 TUI(--tui 등가).
	// 3층 방어 1층 — FLINT_NO_TUI 설정 시 TTY 여부와 무관하게 차단.
	if len(args) == 0 && !tui.NoTUIForced() && pendingTTYFn() {
		return pendingTuiFn(ctx, st, stdout)
	}
	return knowledge.RunPending(ctx, st, args, stdin, stdout)
}
