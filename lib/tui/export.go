// export.go — v1.5.0 T8: `flint export --tui` 대화형 흐름.
// huh 폼(형식 5종·status·출력 경로) → 미리보기(행 수·예상 바이트) →
// 실행은 기존 플래그 경로(cli.runExport)에 조립된 인자를 전달해 재사용
// 한다(중복 구현 금지 — 출력·사이드카·행 수 상한은 기존 경로 계약 그대로
// 자동 적용). 조회·포맷터·상한·실행 의존은 cli 측에서 주입받는다(순환
// 임포트 회피 겸 seam — 테스트는 폼·확인 단계 var로 fake 주입).
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/huh"
)

// ExportSelection — huh 폼 결과(형식·status·출력 경로). Out이 빈 문자열이면
// stdout 출력(stdout 모드).
type ExportSelection struct {
	Format string
	Status string
	Out    string
}

// ExportPreview — 미리보기 결과: 선택된 필터의 행 수·예상 바이트.
type ExportPreview struct {
	Rows  int
	Bytes int
}

// ExportDeps — cli 측 주입 의존. Preview·MaxRows·Run 모두 기존 조회·
// 등록표·실행 seam의 래퍼다(TUI가 원천 로직을 재구현하지 않는다).
type ExportDeps struct {
	// Preview — 선택된 필터로 행 수·예상 바이트 산출(기존 조회·포맷터
	// seam 재사용). nil이면 미리보기·확인 단계를 건너뛴다.
	Preview func(ctx context.Context, sel ExportSelection) (ExportPreview, error)
	// MaxRows — 행 수 상한(등록표 키 export_import_max_rows, 기본값
	// 폴백 포함). nil이면 상한 검사를 생략한다(실행 시 기존 경로가
	// 다시 판정한다).
	MaxRows func(ctx context.Context) int
	// Run — 조립된 인자로 기존 runExport 실행. 반환은 exit 코드.
	Run func(args []string) int
	// CancelNote — 사용자 취소(폼 중단·확인 거절) 시 "export cancelled"
	// 1행 안내를 쓸 출력 대상. nil이면 os.Stdout.
	CancelNote io.Writer
}

// exportFormFn — huh 폼 seam(테스트 fake 주입). 기본 폼: 형식 5종·
// status 2종·출력 경로(선택 — 빈 값 stdout). ctrl+c는 huh.ErrUserAborted.
var exportFormFn = func(sel *ExportSelection) error {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Format").
				Options(huh.NewOptions("jsonl", "md", "txt", "csv", "pam")...).
				Value(&sel.Format),
			huh.NewSelect[string]().
				Title("Status").
				Options(huh.NewOptions("active", "all")...).
				Value(&sel.Status),
			huh.NewInput().
				Title("Output file (optional — empty writes to stdout)").
				Value(&sel.Out),
		).
			// v1.5.2 T1: 비대화 탈출 안내(적응형 기본 진입 안내) — 폼 하단.
			Description(nonInteractiveHint),
	).Run()
}

// exportConfirmFn — 미리보기 확인 seam(테스트 fake 주입). 기본은 huh
// confirm 폼: 행 수·예상 바이트를 보여주고 진행 여부를 묻는다.
var exportConfirmFn = func(pv ExportPreview) (bool, error) {
	proceed := false
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Export %d rows (~%d bytes)?", pv.Rows, pv.Bytes)).
				Description("The existing flag path applies the row limit, checksum sidecar, and summary as-is.").
				Affirmative("Export").
				Negative("Cancel").
				Value(&proceed),
		),
	).Run()
	return proceed, err
}

// ExportArgs — 폼 결과 → 플래그 인자 조립(단일 진실원 — Run 위임 시
// 에도 이 조립을 쓴다). Out이 빈 문자열이면 --out을 붙이지 않는다
// (stdout 모드).
func ExportArgs(sel ExportSelection) []string {
	args := []string{"--format", sel.Format, "--status", sel.Status}
	if sel.Out != "" {
		args = append(args, "--out", sel.Out)
	}
	return args
}

// exportCancelNotice — 사용자 취소 안내 1행(조용한 0바이트 폐지 —
// A3). exit 0은 유지된다.
func exportCancelNotice(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, "export cancelled")
}

// RunExport — export TUI 흐름: 폼 → 미리보기(상한 검사) → 확인 → 기존
// 플래그 경로 실행. 반환 (exitCode, err): err != nil이면 호출부가 오류
// JSON 한 줄 + exit 3으로 폴백한다. 비터미널은 폼 시작 전 즉시 (3, 오류)
// — T5 관례 "TUI requires a terminal". 사용자 중단(ctrl+c)은 "export
// cancelled" 안내 후 실행 없이 (0, nil) — 깨끗한 취소는 오류가 아니다.
func RunExport(ctx context.Context, deps ExportDeps) (int, error) {
	if !isTerminal() {
		return 3, fmt.Errorf("TUI requires a terminal — use flag-mode instead")
	}
	sel := ExportSelection{Format: "jsonl", Status: "active"}
	if err := exportFormFn(&sel); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			exportCancelNotice(deps.CancelNote)
			return 0, nil
		}
		return 3, fmt.Errorf("export tui form failed: %w", err)
	}
	if deps.Preview != nil {
		pv, err := deps.Preview(ctx, sel)
		if err != nil {
			return 3, fmt.Errorf("export preview failed: %w", err)
		}
		if deps.MaxRows != nil {
			if max := deps.MaxRows(ctx); pv.Rows > max {
				return 3, fmt.Errorf("export exceeds max rows: %d > %d (export_import_max_rows) — narrow the filter or raise the limit", pv.Rows, max)
			}
		}
		proceed, err := exportConfirmFn(pv)
		if err != nil && !errors.Is(err, huh.ErrUserAborted) {
			return 3, fmt.Errorf("export tui confirm failed: %w", err)
		}
		if !proceed {
			exportCancelNotice(deps.CancelNote)
			return 0, nil
		}
	}
	if deps.Run == nil {
		return 3, fmt.Errorf("export tui: run delegate not wired")
	}
	return deps.Run(ExportArgs(sel)), nil
}
