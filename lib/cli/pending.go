// pending.go — v1.5.0 T7: `flint pending` 진입 분기. --tui 옵트인 시에만
// TUI(tui.RunPendingTUI)로, 그 외 전부(플래그 없음·--json·unknown flag
// exit 3)는 knowledge.RunPending에 불변 위임한다(파이프·CI 무영향 —
// 비TUI 경로 완전 유지). TUI 런처는 seam var(테스트 fake 주입).
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

func init() {
	// knowledge.init 등록(RunPending)을 덮어쓴다 — wire.Register 최신값
	// 승계(같은 이름 재등록 규약). 기본 경로는 아래 위임으로 불변.
	Register("pending", runPendingEntry)
}

// runPendingEntry — pending 핸들러. --tui 단독만 TUI 분기(조합 금지 —
// TUI는 인자 없는 대화형이므로 나머지 플래그는 무효 인자 exit 3).
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
	return knowledge.RunPending(ctx, st, args, stdin, stdout)
}
