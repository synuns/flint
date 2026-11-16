// report.go — v1.5.0 T9: `flint report --tui` 진입 분기. 등록은 ops
// 패키지 init(RunReport)이 먼저 하고, cli init이 이 래퍼로 재등록해
// 덮는다(Register 후속 승계 — cli가 ops를 import하므로 순서 보장).
// --tui 옵트인: 기존 3뷰(JSON 기본·--text·--html) 경로는 바이트 불변
// 위임. TUI 데이터는 RunReport(JSON 뷰) 수집 산물을 그대로 파싱해
// 쓴다 — 4뷰 동일 원천(중복 수집 구현 금지).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// 수집·화면 seam(테스트 주입 — export 관례 승계: 기본 실구현, 테스트는
// fake 교체, nil 재할당은 기본값 해석).
var (
	// reportCollectFn — ReportOutput 수집(JSON 뷰 경로 재사용 — 산물을
	// stdout 대신 버퍼로 받는 것뿐). 오류 시 (에러 한 줄, exit 코드).
	reportCollectFn = func(ctx context.Context, st Store, args []string, stdin io.Reader) (string, int) {
		var buf strings.Builder
		code := runReport(ctx, st, args, stdin, &buf)
		return buf.String(), code
	}
	// reportTUIFn — 화면 기동(비터미널 오류 반환 — 호출부 exit 3).
	reportTUIFn = tui.RunReportTUI
	// reportTTYFn — v1.5.2 T1 적응형 진입 TTY 판정 seam(기본 실구현
	// tui.IsTerminalStdout, 테스트 fake 교체).
	reportTTYFn = tui.IsTerminalStdout
)

func init() { Register("report", runReportEntry) }

// runReportEntry — `flint report [--tui] [기존 플래그...]`. 진입 규칙
// (v1.5.2 T1 통일): ① --tui 명시 → TUI(비터미널 폴백 exit 3 불변)
// ② 무인자·무플래그 + 터미널 → TUI ③ 무인자 + 비터미널 → 기본 JSON 뷰
// ④ 플래그 존재 → 기존 경로. --tui는 뷰 플래그(--html·--text·--out)와
// 조합 불가(exit 3 — 화면·파일 이원 방지). TUI 경로: 수집(JSON 원천) →
// ReportOutput 파싱 → TUI. 창 일수는 수집 산물 행 수에서 유도(상한
// clamp가 반영된 실제 창).
func runReportEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	tuiIdx := -1
	for i, a := range args {
		if a == "--tui" {
			tuiIdx = i
			break
		}
	}
	// v1.5.2 T1 적응형 전환: 인자·플래그 전혀 없음 + 터미널이면 TUI
	// (--tui 등가 — rest가 빈 상태여 뷰 플래그 검증도 자동 통과).
	// 3층 방어 1층 — FLINT_NO_TUI 설정 시 TTY 여부와 무관하게 차단.
	if tuiIdx < 0 && !(len(args) == 0 && !tui.NoTUIForced() && reportTTYFn()) {
		return runReport(ctx, st, args, stdin, stdout) // 기본 경로 불변 위임
	}
	rest := args
	if tuiIdx >= 0 {
		rest = append(append([]string{}, args[:tuiIdx]...), args[tuiIdx+1:]...)
	}
	for _, a := range rest {
		switch a {
		case "--html", "--text", "--out":
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --tui cannot combine with %s — usage: flint report [--tui] | [--days N] [--html] [--out FILE] [--text]", a)))
			return 3
		}
	}
	// --out 다음 토큰 검사는 불필요 — --out 값이 먼저 오는 조합은
	// RunReport 인자 검증이 이미 exit 3으로 잡는다(--out은 값을 흡수).
	line, code := reportCollectFn(ctx, st, rest, stdin)
	if code != 0 {
		fmt.Fprint(stdout, line) // 수집 산물 에러 한 줄 전달(개행 포함)
		return code
	}
	var rep ops.ReportOutput
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &rep); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("report: --tui parse: %v", err)))
		return 1
	}
	run := reportTUIFn
	if run == nil {
		run = tui.RunReportTUI
	}
	if err := run(rep, len(rep.Days)); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}
