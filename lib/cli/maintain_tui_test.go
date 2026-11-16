// maintain_tui_test.go — v1.6.0 T6: `flint maintain --tui` 진입 분기 계약
// 테스트. 옵트인만(적응형 X — 유령 인자 회귀 방지): --tui 단독만 TUI
// (tui.RunMaintainTUI), 조합은 무효 인자 exit 3, FLINT_NO_TUI는 3층 방어
// 1층으로 차단. 그 외 전부 ops.RunMaintain 불변 위임(회귀).
package cli

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// installMaintainTuiFake — maintainTuiFn seam 교체(호출 기록 fake).
func installMaintainTuiFake(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := maintainTuiFn
	maintainTuiFn = func(ctx context.Context, st wire.Store, stdout io.Writer) int {
		calls = append(calls, []string{"tui"})
		return 42
	}
	t.Cleanup(func() { maintainTuiFn = orig })
	return &calls
}

// installMaintainOpsFake — maintainOpsRun seam 교체(호출 인자 기록 fake).
func installMaintainOpsFake(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := maintainOpsRun
	maintainOpsRun = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		calls = append(calls, args)
		return 7
	}
	t.Cleanup(func() { maintainOpsRun = orig })
	return &calls
}

// RF-protocol — --tui 단독: TUI seam 호출·exit 전파.
func TestMaintainEntryTUIBranch(t *testing.T) {
	calls := installMaintainTuiFake(t)
	opsCalls := installMaintainOpsFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 42 {
		t.Fatalf("--tui exit = %d, want seam-returned 42", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("TUI seam calls = %d, want 1", len(*calls))
	}
	if len(*opsCalls) != 0 {
		t.Fatalf("ops calls = %d, want 0", len(*opsCalls))
	}
}

// RF-protocol — --tui + 다른 플래그: 무효 인자 exit 3(단독만 허용).
func TestMaintainEntryTUIRejectsExtraFlags(t *testing.T) {
	calls := installMaintainTuiFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, []string{"--tui", "--check"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("--tui --check exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "--tui") {
		t.Fatalf("output = %q, want --tui combination error", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected combo must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — FLINT_NO_TUI(3층 방어 1층): 명시 --tui도 차단 exit 3.
func TestMaintainEntryNoTUIEnvBlocksExplicitTUI(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	calls := installMaintainTuiFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("FLINT_NO_TUI --tui exit = %d, want 3", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("FLINT_NO_TUI must block TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — 옵트인만: 무인자는 적응형 TUI 없이 ops 위임 유지(유령
// 인자 방지 — bare maintain은 기존 usage exit 3).
func TestMaintainEntryNoAdaptiveBareRun(t *testing.T) {
	calls := installMaintainTuiFake(t)
	opsCalls := installMaintainOpsFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, nil,
		strings.NewReader(""), &out)
	if code != 7 {
		t.Fatalf("bare exit = %d, want ops-returned 7", code)
	}
	if len(*opsCalls) != 1 || len((*opsCalls)[0]) != 0 {
		t.Fatalf("ops calls = %v, want 1 with no args", *opsCalls)
	}
	if len(*calls) != 0 {
		t.Fatalf("bare run must not reach TUI seam (opt-in only), calls = %d", len(*calls))
	}
}

// RF-protocol — 기존 플래그 회귀: --check 등은 인자 그대로 ops 위임.
func TestMaintainEntryFlagsDelegateUnchanged(t *testing.T) {
	calls := installMaintainTuiFake(t)
	opsCalls := installMaintainOpsFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, []string{"--check"},
		strings.NewReader(""), &out)
	if code != 7 {
		t.Fatalf("--check exit = %d, want ops-returned 7", code)
	}
	want := []string{"--check"}
	if len(*opsCalls) != 1 || strings.Join((*opsCalls)[0], " ") != strings.Join(want, " ") {
		t.Fatalf("ops args = %v, want %v", *opsCalls, want)
	}
	if len(*calls) != 0 {
		t.Fatalf("--check must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — seam 기본값: TUI 런처 = tui.RunMaintainTUI, ops 위임 =
// ops.RunMaintain(중복 구현 금지 — 옵트인 진입만 이 파일 소유).
func TestMaintainEntrySeamDefaults(t *testing.T) {
	if reflect.ValueOf(maintainTuiFn).Pointer() != reflect.ValueOf(tui.RunMaintainTUI).Pointer() {
		t.Fatal("maintainTuiFn seam must default to tui.RunMaintainTUI")
	}
	if reflect.ValueOf(maintainOpsRun).Pointer() != reflect.ValueOf(ops.RunMaintain).Pointer() {
		t.Fatal("maintainOpsRun seam must default to ops.RunMaintain")
	}
}
