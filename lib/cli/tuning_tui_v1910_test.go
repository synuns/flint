// tuning_tui_v1910_test.go — v1.9.3 T3 `flint tuning --tui` 옵트인
// 계약 테스트. 진입 분기: 무인자·기존 서브커맨드는 ops.RunTuning 바이트
// 불변 위임(적응형 진입 없음 — 무인자 list JSON은 에이전트 계약),
// --tui 단독만 TUI, 조합은 무효 인자 exit 3, FLINT_NO_TUI는 3층 방어
// 1층 차단. TUI 흐름(tui.RunTuningTUI): 비터미널 exit 3
// (ErrTerminalRequired 관례), 폼 확정은 기존 set/reset 표준 경로 위임
// (fake seam·기본 경로 실위임), ctrl+c 취소 위생. pty 실구동 1회
// (터미널 진입·폼 렌더·종료 위생).
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// installTuningTuiFake — tuningTuiFn seam 교체(호출 기록 fake).
func installTuningTuiFake(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := tuningTuiFn
	tuningTuiFn = func(ctx context.Context, st wire.Store, stdout io.Writer, deps tui.TuningDeps) int {
		calls++
		return 42
	}
	t.Cleanup(func() { tuningTuiFn = orig })
	return &calls
}

// installTuningOpsFake — tuningOpsRun seam 교체(호출 인자 기록 fake).
func installTuningOpsFake(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := tuningOpsRun
	tuningOpsRun = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		calls = append(calls, args)
		return 7
	}
	t.Cleanup(func() { tuningOpsRun = orig })
	return &calls
}

// RF-protocol — --tui 단독: TUI seam 호출·exit 전파.
func TestTuningEntryTUIBranch(t *testing.T) {
	calls := installTuningTuiFake(t)
	opsCalls := installTuningOpsFake(t)
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), &fakeStore{}, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 42 {
		t.Fatalf("--tui exit = %d, want seam-returned 42", code)
	}
	if *calls != 1 {
		t.Fatalf("TUI seam calls = %d, want 1", *calls)
	}
	if len(*opsCalls) != 0 {
		t.Fatalf("ops calls = %d, want 0", len(*opsCalls))
	}
}

// RF-protocol — --tui + 다른 인자: 무효 인자 exit 3(단독만 허용 —
// 위치 서브커맨드(set/get/reset)와의 유령 인자 조합 방지).
func TestTuningEntryTUIRejectsExtraArgs(t *testing.T) {
	calls := installTuningTuiFake(t)
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), &fakeStore{}, []string{"--tui", "set"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("--tui set exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "--tui") {
		t.Fatalf("output = %q, want --tui combination error", out.String())
	}
	if *calls != 0 {
		t.Fatalf("rejected combo must not reach TUI seam, calls = %d", *calls)
	}
}

// RF-protocol — FLINT_NO_TUI(3층 방어 1층): 명시 --tui도 차단 exit 3.
func TestTuningEntryNoTUIEnvBlocksExplicitTUI(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	calls := installTuningTuiFake(t)
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), &fakeStore{}, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("FLINT_NO_TUI --tui exit = %d, want 3", code)
	}
	if *calls != 0 {
		t.Fatalf("FLINT_NO_TUI must block TUI seam, calls = %d", *calls)
	}
}

// RF-protocol — 옵트인만: 무인자는 적응형 TUI 없이 ops 위임 유지
// (무인자 = list JSON — 에이전트·스크립트 기존 경로 절대 보장).
func TestTuningEntryNoAdaptiveBareRun(t *testing.T) {
	calls := installTuningTuiFake(t)
	opsCalls := installTuningOpsFake(t)
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), &fakeStore{}, nil,
		strings.NewReader(""), &out)
	if code != 7 {
		t.Fatalf("bare exit = %d, want ops-returned 7", code)
	}
	if len(*opsCalls) != 1 || len((*opsCalls)[0]) != 0 {
		t.Fatalf("ops calls = %v, want 1 with no args", *opsCalls)
	}
	if *calls != 0 {
		t.Fatalf("bare run must not reach TUI seam (opt-in only), calls = %d", *calls)
	}
}

// RF-protocol — 기존 서브커맨드 회귀: set/get/reset/list 등은 인자 그대로
// ops 위임.
func TestTuningEntrySubcommandsDelegateUnchanged(t *testing.T) {
	calls := installTuningTuiFake(t)
	opsCalls := installTuningOpsFake(t)
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), &fakeStore{}, []string{"set", "dead_days", "45"},
		strings.NewReader(""), &out)
	if code != 7 {
		t.Fatalf("set exit = %d, want ops-returned 7", code)
	}
	want := []string{"set", "dead_days", "45"}
	if len(*opsCalls) != 1 || strings.Join((*opsCalls)[0], " ") != strings.Join(want, " ") {
		t.Fatalf("ops args = %v, want %v", *opsCalls, want)
	}
	if *calls != 0 {
		t.Fatalf("set must not reach TUI seam, calls = %d", *calls)
	}
}

// RF-protocol — seam 기본값: TUI 런처 = tui.RunTuningTUI, ops 위임 =
// ops.RunTuning(중복 구현 금지 — 옵트인 진입만 이 파일 소유).
func TestTuningEntrySeamDefaults(t *testing.T) {
	if reflect.ValueOf(tuningTuiFn).Pointer() != reflect.ValueOf(tui.RunTuningTUI).Pointer() {
		t.Fatal("tuningTuiFn seam must default to tui.RunTuningTUI")
	}
	if reflect.ValueOf(tuningOpsRun).Pointer() != reflect.ValueOf(ops.RunTuning).Pointer() {
		t.Fatal("tuningOpsRun seam must default to ops.RunTuning")
	}
}

// RF-protocol — 바이트 동치: 무인자·기존 서브커맨드 전 경로가
// ops.RunTuning 직접 호출과 출력·exit 바이트 불변(위임이지 재구현이
// 아님을 증명).
func TestTuningEntryByteParityWithOps(t *testing.T) {
	cases := [][]string{
		nil,
		{"list"},
		{"get", "dead_days"},
		{"set", "dead_days", "45"},
		{"set", "dead_days", "not-a-number"},
		{"reset", "dead_days"},
		{"reset", "--all"},
		{"get", "no_such_key"},
		{"frobnicate"},
	}
	for _, args := range cases {
		fEntry, fDirect := newTuningFake(), newTuningFake()
		var oEntry, oDirect bytes.Buffer
		cEntry := runTuningEntry(context.Background(), fEntry, args,
			strings.NewReader(""), &oEntry)
		cDirect := ops.RunTuning(context.Background(), fDirect, args,
			strings.NewReader(""), &oDirect)
		if cEntry != cDirect {
			t.Fatalf("args=%v exit %d != ops %d (entry out: %s)", args, cEntry, cDirect, oEntry.String())
		}
		if oEntry.String() != oDirect.String() {
			t.Fatalf("args=%v output diverged:\nentry: %s\ndirect: %s", args, oEntry.String(), oDirect.String())
		}
	}
}

// RF-protocol — 비터미널 --tui: 실경로(tui.RunTuningTUI 기본 배선)에서
// 폼 시작 전 즉시 exit 3(ErrTerminalRequired 관례 — go test stdout은
// 터미널이 아님). 스토어 조회도 일어나지 않는다(nil store로도 진입
// 직후 차단).
func TestTuningEntryTUIRequiresTerminalRealPath(t *testing.T) {
	var out bytes.Buffer
	code := runTuningEntry(context.Background(), nil, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("non-terminal --tui exit = %d, want 3 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "TUI requires a terminal") {
		t.Fatalf("output = %q, want ErrTerminalRequired message", out.String())
	}
}

// RF-state — 폼 확정 → 기존 set 경로 위임(fake seam): Form이 채운
// 선택(키·값·set)이 deps.Run에 ["set", key, value]로 전달되고 exit 코드가
// 그대로 전파된다.
func TestRunTuningTUIFormConfirmDelegatesSet(t *testing.T) {
	var gotArgs []string
	var out bytes.Buffer
	code := tui.RunTuningTUI(context.Background(), nil, &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "45", "set"
			return nil
		},
		Run: func(args []string) int {
			gotArgs = append([]string{}, args...)
			return 7
		},
	})
	if code != 7 {
		t.Fatalf("exit = %d, want Run-returned 7", code)
	}
	want := []string{"set", "dead_days", "45"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("Run args = %v, want %v", gotArgs, want)
	}
}

// RF-state — reset 확정: 값 없이 ["reset", key]만 전달.
func TestRunTuningTUIFormConfirmDelegatesReset(t *testing.T) {
	var gotArgs []string
	var out bytes.Buffer
	code := tui.RunTuningTUI(context.Background(), nil, &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "45", "reset"
			return nil
		},
		Run: func(args []string) int {
			gotArgs = append([]string{}, args...)
			return 0
		},
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := []string{"reset", "dead_days"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("Run args = %v, want %v", gotArgs, want)
	}
}

// RF-state — 기본 Run 위임(deps.Run nil): 실 ops.RunTuning set 표준
// 경로가 재사용된다 — JSON 1행 출력({"status":"set",...})·실경로 값
// 검증·meta 기록 전부 기존 경로 소유(중복 구현 금지 증명).
func TestRunTuningTUIDefaultRunReusesStandardSetPath(t *testing.T) {
	f := newTuningFake()
	var out bytes.Buffer
	code := tui.RunTuningTUI(context.Background(), f, &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "45", "set"
			return nil
		},
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), `{"status":"set","key":"dead_days","value":"45"}`) {
		t.Fatalf("output = %q, want standard set JSON 1-line", out.String())
	}
	if len(f.sets) != 1 || f.meta["dead_days"] != "45" {
		t.Fatalf("meta not written through standard path: sets=%v meta=%v", f.sets, f.meta)
	}
	// 무효 값도 표준 경로 검증(실경로 파싱 폴백 거부·안내)으로 exit 3.
	out.Reset()
	code = tui.RunTuningTUI(context.Background(), newTuningFake(), &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "many", "set"
			return nil
		},
	})
	if code != 3 {
		t.Fatalf("invalid value exit = %d, want 3 (standard-path rejection)", code)
	}
	if !strings.Contains(out.String(), "dead_days") || !strings.Contains(out.String(), "TUNING") {
		t.Fatalf("output = %q, want fallback-rejection guidance", out.String())
	}
}

// RF-protocol — ctrl+c 취소(huh.ErrUserAborted): "tuning cancelled" 안내
// 1행 후 exit 0, 실행 위임 없음 — 깨끗한 취소.
func TestRunTuningTUIUserAbortExitsCleanly(t *testing.T) {
	var out bytes.Buffer
	ran := false
	code := tui.RunTuningTUI(context.Background(), nil, &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			return huh.ErrUserAborted
		},
		Run: func(args []string) int {
			ran = true
			return 1
		},
	})
	if code != 0 {
		t.Fatalf("abort exit = %d, want 0", code)
	}
	if ran {
		t.Fatal("Run must not execute after abort")
	}
	if !strings.Contains(out.String(), "tuning cancelled") {
		t.Fatalf("output = %q, want 'tuning cancelled' notice", out.String())
	}
}

// RF-protocol — 키 행 로드 실패: 표준 오류 경로(ErrLine + MapExit)로
// 폼 없이 종료.
func TestRunTuningTUIRowsFailureUsesStandardExit(t *testing.T) {
	var out bytes.Buffer
	dbErr := errors.New("db unreachable")
	code := tui.RunTuningTUI(context.Background(), nil, &out, tui.TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
			return nil, dbErr
		},
		Form: func(sel *tui.TuningSelection, rows []tui.TuningKeyRow) error {
			t.Fatal("form must not open when rows fail to load")
			return nil
		},
	})
	if code != store.MapExit(dbErr) {
		t.Fatalf("exit = %d, want MapExit(%d)", code, store.MapExit(dbErr))
	}
	if !strings.Contains(out.String(), "db unreachable") {
		t.Fatalf("output = %q, want ErrLine with cause", out.String())
	}
}

// TestTuningTUIPtyRealRun — pty 실구동 1회: 실 터미널에서 기본 huh 폼이
// 렌더되고(터미널 진입 통과) ctrl+c 종료가 "tuning cancelled" 안내·
// exit 0으로 위생 처리되는지. 자식 프로세스(FLINT_TUNING_TUI_PTY_CHILD=1)
// 는 실 폼· Rows/Run fake로 tui.RunTuningTUI를 실행하고, 부모는
// `script`(BSD pty 래퍼)로 구동해 산출물·종료 상태를 검증한다. script
// 부재 환경은 스킵(pty 실구동은 이 1회로 한정).
func TestTuningTUIPtyRealRun(t *testing.T) {
	// pty 실구동은 로컬/발행 전 TUI 표준 검증 — CI headless 러너의
	// 단말 에뮬레이션 타이밍 차이로 회귀 판정이 불안정해 CI는 스킵.
	if os.Getenv("CI") == "true" {
		t.Skip("pty 실구동: CI 환경 제외(로컬 TUI 표준 검증)")
	}
	if os.Getenv("FLINT_TUNING_TUI_PTY_CHILD") == "1" {
		code := tui.RunTuningTUI(context.Background(), nil, os.Stdout, tui.TuningDeps{
			Rows: func(ctx context.Context) ([]tui.TuningKeyRow, error) {
				return []tui.TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30", Consumer: "prune"}}, nil
			},
			Run: func(args []string) int { return 0 },
		})
		fmt.Fprintf(os.Stdout, "\nPTY-CHILD-EXIT %d\n", code)
		os.Exit(code)
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(pty wrapper) not available — pty real-run skipped")
	}
	// script의 pty 크기는 부모 tty에서 상속되는데 go test stdout은
	// 파이프라 0x0이 된다(폭 0 렌더 → 빈 필드). sh -c로 pty 크기를
	// 지정한 뒤 자식을 실행해 실제 터미널과 같은 조건을 만든다.
	cmd := exec.Command(script, "-q", "/dev/null", "/bin/sh", "-c",
		`stty rows 40 cols 120 2>/dev/null; exec "$0" -test.run=^TestTuningTUIPtyRealRun$`,
		os.Args[0])
	cmd.Env = append(os.Environ(), "FLINT_TUNING_TUI_PTY_CHILD=1")
	if os.Getenv("TERM") == "" {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pty child: %v", err)
	}
	// 렌더 대기 — 폼 제목("Tuning")·키 행("dead_days")이 보일 때까지 폴링.
	// bubbletea는 첫 페인트 전 터미널 질의(OSC 11 배경색·DSR 커서 위치)를
	// 보내고 응답을 기다린다 — script의 pty에는 응답자가 없으므로 부모가
	// 실제 터미널처럼 응답한다(에뮬레이터 최소 동작).
	deadline := time.Now().Add(20 * time.Second)
	rendered, answeredBG, answeredCPR := false, false, false
	for time.Now().Before(deadline) {
		s := out.String()
		if !answeredBG && strings.Contains(s, "\x1b]11;?") {
			answeredBG = true
			stdin.Write([]byte("\x1b]11;rgb:1c1c/1c1c/1c1c\x1b\\"))
		}
		if !answeredCPR && strings.Contains(s, "\x1b[6n") {
			answeredCPR = true
			stdin.Write([]byte("\x1b[1;1R"))
		}
		if strings.Contains(s, "dead_days") {
			rendered = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !rendered {
		cmd.Process.Kill()
		t.Fatalf("form did not render in pty output: %q", out.String())
	}
	// ctrl+c(0x03) → huh 중단 → 안내·exit 0 위생.
	if _, err := stdin.Write([]byte{3}); err != nil {
		cmd.Process.Kill()
		t.Fatalf("send ctrl+c: %v", err)
	}
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		t.Fatal("pty child did not exit after ctrl+c")
	}
	got := out.String()
	if !strings.Contains(got, "tuning cancelled") {
		t.Errorf("pty output missing cancel notice: %q", tailFor(got, 200))
	}
	if !strings.Contains(got, "PTY-CHILD-EXIT 0") {
		t.Errorf("pty output missing clean-exit sentinel: %q", tailFor(got, 200))
	}
}

// tailFor — 진단용 출력 꼬리(마지막 n바이트).
func tailFor(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
