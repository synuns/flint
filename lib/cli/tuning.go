// tuning.go — `flint tuning --tui` 진입 분기. 옵트인만(적응형 전환
// 없음 — 무인자 list JSON은 에이전트·스크립트 계약이라 불변): --tui
// 단독만 TUI(tui.RunTuningTUI)로, 조합은 무효 인자 exit 3(maintain 관례
// 승계 — tuning도 위치 서브커맨드 set/get/reset을 받아 유령 인자 조합
// 방지). FLINT_NO_TUI는 3층 방어 1층 — 명시 --tui도 차단 후 기존 경로
// 안내. 그 외 전부 ops.RunTuning에 바이트 불변 위임한다(파이프·CI
// 무영향). TUI 런처·ops 위임은 seam var(테스트 fake 주입).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// tuningTuiFn — TUI 진입 seam. 기본 실구현, 테스트 fake 교체.
var tuningTuiFn = tui.RunTuningTUI

// tuningOpsRun — 기존 tuning 경로 위임 seam(ops.RunTuning — 중복
// 구현 금지). 기본 실구현, 진입 분기 테스트 fake 교체.
var tuningOpsRun = ops.RunTuning

func init() {
	// ops.init 등록(RunTuning)을 덮어쓴다 — wire.Register 최신값 승계
	// (같은 이름 재등록 규약, pending·maintain·report 관례). 기본 경로는
	// 아래 위임으로 불변(--tui 분기만 이 파일 소유).
	Register("tuning", runTuningEntry)
}

// runTuningEntry — tuning 핸들러. 진입 규칙: ① --tui 단독 → TUI(조합은
// 무효 인자 exit 3·FLINT_NO_TUI 설정 시 차단 exit 3·비터미널은 TUI
// 런처 폴백 exit 3 불변) ② 그 외(무인자·서브커맨드 전부) →
// ops.RunTuning 현행 경로 — 옵트인만이라 무인자 + 터미널 적응형 전환은
// 없다(무인자 = list JSON 불변).
func runTuningEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					"tuning: --tui cannot be combined with %s — run it alone", b)))
				return 3
			}
		}
		// 3층 방어 1층 — FLINT_NO_TUI 설정 시 TTY 여부와 무관하게 차단
		// (에이전트·CI 명시 탈출구 우선).
		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"tuning: --tui is blocked by FLINT_NO_TUI — use tuning flag-mode subcommands")))
			return 3
		}
		return tuningTuiFn(ctx, st, stdout, tui.TuningDeps{})
	}
	return tuningOpsRun(ctx, st, args, stdin, stdout)
}
