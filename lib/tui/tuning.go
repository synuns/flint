// tuning.go — `flint tuning --tui` 대화형 폼(huh). 키 선택 → 값 편집 →
// set/reset 확정. 키 목록은 TuningKeys 단일 소스(ops 등록표)에서, 값
// 검증(loadThresholds→ParseThresholds 실경로 — 폴백 값 거부·안내)과
// JSON 1행 출력은 기존 ops.RunTuning set/reset 표준 경로에 그대로 위임한다
// (중복 구현 금지). 비터미널 --tui는 폼 시작 전 즉시 exit 3
// (ErrTerminalRequired 관례). 진입 분기(옵트인-only·FLINT_NO_TUI 차단)는
// lib/cli/tuning.go 소유 — 여기는 TUI 흐름만.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// TuningKeyRow — 폼 키 선택 행(현재값·기본값 표시). TuningKeys 단일
// 소스에서 파생된다.
type TuningKeyRow struct {
	Key      string
	Value    string
	Default  string
	Consumer string
}

// TuningSelection — huh 폼 확정 결과. Action "set"은 Value 사용,
// "reset"은 기본값 복귀(Value 미사용).
type TuningSelection struct {
	Key    string
	Value  string
	Action string
}

// TuningDeps — 진입 분기(cli)·테스트에서 주입하는 의존(export
// ExportDeps 관례). nil 필드는 기본 실구현으로 해석된다.
type TuningDeps struct {
	// Rows — 키 행 로드. 기본: ops.TuningKeys × ops.TuningCurrent 실경로.
	Rows func(ctx context.Context) ([]TuningKeyRow, error)
	// Form — huh 폼. 기본: 키 선택 → 값 편집 → set/reset 선택 단일 그룹.
	Form func(sel *TuningSelection, rows []TuningKeyRow) error
	// IsTerminal — 터미널 판정. 기본: tui 내부 판정(isTerminal seam).
	// FLINT_NO_TUI 검사는 cli 3층 방어 1층이 이미 소유(원시 판정 사용).
	IsTerminal func() bool
	// Run — 기존 set/reset 표준 경로 실행 위임(기본 ops.RunTuning —
	// JSON 1행 출력·실경로 값 검증·exit 코드 전부 표준 경로 재사용).
	Run func(args []string) int
	// CancelNote — 사용자 취소(ctrl+c) 안내 1행 출력 대상. 기본 호출부
	// stdout(TUI 종료 후 채널 — 실구현에서는 os.Stdout과 동일).
	CancelNote io.Writer
}

// tuningRunner — 기본 Run 위임 원천 seam(ops.RunTuning — 중복 구현
// 금지). deps.Run이 nil일 때 st·stdout을 묶어 호출한다.
var tuningRunner = ops.RunTuning

// tuningKeyRows — 기본 키 행 로드: TuningKeys 단일 소스에 현재값
// (TuningCurrent — loadThresholds→ParseThresholds 실경로 산물)을 조합,
// 결정적 정렬(list 표기와 동일 규약).
func tuningKeyRows(ctx context.Context, st wire.Store) ([]TuningKeyRow, error) {
	th, _, err := ops.TuningCurrent(ctx, st)
	if err != nil {
		return nil, err
	}
	keys := ops.TuningKeys()
	rows := make([]TuningKeyRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, TuningKeyRow{
			Key: k.Key, Value: k.Get(th), Default: k.Default, Consumer: k.Consumer,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// tuningFormFn — 기본 huh 폼: 키 선택(행 라벨에 현재값·기본값 표시) →
// 값 편집(선택 키의 현재값으로 prefilled) → set/reset 선택. 값 검증은
// 폼이 아니라 확정 위임 시 표준 경로(tuningSet 실경로 파싱)가 소유한다 —
// 폼 검증 규칙의 이중 유지를 하지 않는다(중복 구현 금지). ctrl+c는
// huh.ErrUserAborted.
var tuningFormFn = func(sel *TuningSelection, rows []TuningKeyRow) error {
	opts := make([]huh.Option[string], 0, len(rows))
	for _, r := range rows {
		opts = append(opts, huh.NewOption(
			fmt.Sprintf("%s — now %s (default %s)", r.Key, r.Value, r.Default), r.Key))
	}
	// 프리필: 첫 키의 현재값·기본 액션(선택 변경 시 값은 편집으로 갱신).
	if sel.Key == "" && len(rows) > 0 {
		sel.Key, sel.Value = rows[0].Key, rows[0].Value
	}
	if sel.Action == "" {
		sel.Action = "set"
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Key").Options(opts...).Value(&sel.Key),
			huh.NewInput().Title("Value (edit the current value)").Value(&sel.Value),
			huh.NewSelect[string]().Title("Action").
				Options(huh.NewOptions("set", "reset")...).Value(&sel.Action),
		).Title("Tuning").Description(nonInteractiveHint),
	).Run()
}

// RunTuningTUI — `flint tuning --tui` 진입. 비터미널은 폼 시작 전 즉시
// ErrLine(ErrTerminalRequired) + exit 3(Run 래퍼 관례 — 오류 분류
// error:"usage"). 키 행 로드 실패도 표준 오류 경로(ErrLine + MapExit).
// 폼 확정은 deps.Run(기본 ops.RunTuning set/reset)에 위임한다 — JSON
// 1행 출력·실경로 값 검증(폴백 거부·안내)·exit 코드 전부 표준 경로
// 재사용. 사용자 중단(ctrl+c, huh.ErrUserAborted)은 "tuning cancelled"
// 안내 1행 후 exit 0 — 깨끗한 취소는 오류가 아니다(export 관례).
func RunTuningTUI(ctx context.Context, st wire.Store, stdout io.Writer, deps TuningDeps) int {
	term := isTerminal
	if deps.IsTerminal != nil {
		term = deps.IsTerminal
	}
	if !term() {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%w", ErrTerminalRequired)))
		return 3
	}
	loadRows := deps.Rows
	if loadRows == nil {
		loadRows = func(ctx context.Context) ([]TuningKeyRow, error) {
			return tuningKeyRows(ctx, st)
		}
	}
	rows, err := loadRows(ctx)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("tuning: no keys registered")))
		return 1
	}
	form := deps.Form
	if form == nil {
		form = tuningFormFn
	}
	sel := TuningSelection{}
	if err := form(&sel, rows); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			w := deps.CancelNote
			if w == nil {
				w = stdout
			}
			fmt.Fprintln(w, "tuning cancelled")
			return 0
		}
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("tuning tui form failed: %w", err)))
		return 3
	}
	run := deps.Run
	if run == nil {
		so, stc := stdout, st
		run = func(args []string) int {
			return tuningRunner(ctx, stc, args, strings.NewReader(""), so)
		}
	}
	var args []string
	if sel.Action == "reset" {
		args = []string{"reset", sel.Key}
	} else {
		args = []string{"set", sel.Key, sel.Value}
	}
	return run(args)
}
