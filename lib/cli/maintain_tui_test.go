// maintain_tui_test.go — contract tests for the `flint maintain --tui` entry
// branching. Opt-in only (no adaptive switching — preventing ghost-argument regressions): --tui standalone only goes to the TUI
// (tui.RunMaintainTUI); combinations are invalid-arg exit 3; FLINT_NO_TUI blocks as layer 1 of the
// 3-layer guard. Everything else delegates unchanged to ops.RunMaintain (regression).
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

// installMaintainTuiFake — swaps the maintainTuiFn seam (call-recording fake).
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

// installMaintainOpsFake — swaps the maintainOpsRun seam (fake recording call args).
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

// RF-protocol — --tui standalone: TUI seam called, exit propagates.
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

// RF-protocol — --tui + other flags: invalid-arg exit 3 (standalone only).
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

// RF-protocol — FLINT_NO_TUI (layer 1 of the 3-layer guard): even an explicit --tui is blocked, exit 3.
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

// RF-protocol — opt-in only: no-arg keeps the ops delegation without adaptive TUI (preventing ghost
// arguments — bare maintain keeps the existing usage exit 3).
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

// RF-protocol — existing-flag regression: --check etc. delegate to ops with the args as-is.
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

// RF-protocol — seam defaults: TUI launcher = tui.RunMaintainTUI, ops delegation =
// ops.RunMaintain (no duplicate implementation — this file owns only the opt-in entry).
func TestMaintainEntrySeamDefaults(t *testing.T) {
	if reflect.ValueOf(maintainTuiFn).Pointer() != reflect.ValueOf(tui.RunMaintainTUI).Pointer() {
		t.Fatal("maintainTuiFn seam must default to tui.RunMaintainTUI")
	}
	if reflect.ValueOf(maintainOpsRun).Pointer() != reflect.ValueOf(ops.RunMaintain).Pointer() {
		t.Fatal("maintainOpsRun seam must default to ops.RunMaintain")
	}
}

// RF-protocol — v1.13.0 T4: --age-audit (a report mode like --stale)
// delegates to ops with the args as-is (no TUI interception, no ghost
// argument) — the entry-branch regression for the new mode.
func TestMaintainEntryAgeAuditDelegates(t *testing.T) {
	calls := installMaintainTuiFake(t)
	opsCalls := installMaintainOpsFake(t)
	var out bytes.Buffer
	code := runMaintainEntry(context.Background(), &fakeStore{}, []string{"--age-audit"},
		strings.NewReader(""), &out)
	if code != 7 {
		t.Fatalf("--age-audit exit = %d, want ops-returned 7", code)
	}
	want := []string{"--age-audit"}
	if len(*opsCalls) != 1 || strings.Join((*opsCalls)[0], " ") != strings.Join(want, " ") {
		t.Fatalf("ops args = %v, want %v", *opsCalls, want)
	}
	if len(*calls) != 0 {
		t.Fatalf("--age-audit must not reach TUI seam, calls = %d", len(*calls))
	}
}
