// tuning_tui_v1910_test.go — v1.9.3 T3 contract tests for the `flint tuning --tui` opt-in.
// Entry branching: no-arg and existing subcommands delegate byte-for-byte unchanged to ops.RunTuning
// (no adaptive entry — the no-arg list JSON is an agent contract),
// --tui standalone only goes to the TUI, combinations are invalid-arg exit 3, and FLINT_NO_TUI is the layer-1
// block of the 3-layer guard. The TUI flow (tui.RunTuningTUI): non-terminal exit 3
// (the ErrTerminalRequired convention); form confirmation delegates to the existing set/reset standard path
// (fake seam; real delegation on the default path); ctrl+c cancel hygiene. One real pty run
// (terminal entry, form render, exit hygiene).
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
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// installTuningTuiFake — swaps the tuningTuiFn seam (call-recording fake).
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

// installTuningOpsFake — swaps the tuningOpsRun seam (fake recording call args).
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

// RF-protocol — --tui standalone: TUI seam called, exit propagates.
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

// RF-protocol — --tui + other args: invalid-arg exit 3 (standalone only —
// prevents ghost-argument combinations with the positional subcommands set/get/reset).
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

// RF-protocol — FLINT_NO_TUI (layer 1 of the 3-layer guard): even an explicit --tui is blocked, exit 3.
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

// RF-protocol — opt-in only: no-arg keeps the ops delegation without adaptive TUI
// (no-arg = list JSON — the existing agent/script path is absolutely guaranteed).
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

// RF-protocol — existing subcommand regression: set/get/reset/list etc. keep their args and
// delegate to ops.
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

// RF-protocol — seam defaults: TUI launcher = tui.RunTuningTUI, ops delegation =
// ops.RunTuning (no duplicate implementation — this file owns only the opt-in entry).
func TestTuningEntrySeamDefaults(t *testing.T) {
	if reflect.ValueOf(tuningTuiFn).Pointer() != reflect.ValueOf(tui.RunTuningTUI).Pointer() {
		t.Fatal("tuningTuiFn seam must default to tui.RunTuningTUI")
	}
	if reflect.ValueOf(tuningOpsRun).Pointer() != reflect.ValueOf(ops.RunTuning).Pointer() {
		t.Fatal("tuningOpsRun seam must default to ops.RunTuning")
	}
}

// RF-protocol — byte equivalence: every no-arg and existing-subcommand path is byte-for-byte
// identical in output and exit to calling ops.RunTuning directly (proving it is a delegation,
// not a reimplementation).
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

// RF-protocol — non-terminal --tui: on the real wiring (tui.RunTuningTUI default) it exits 3
// immediately before the form starts (the ErrTerminalRequired convention — go test stdout is
// not a terminal). No store lookup happens either (blocked right after entry even with a nil
// store).
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

// RF-state — form confirmation -> delegates to the existing set path (fake seam): the Form-filled
// selection (key, value, set) is passed to deps.Run as ["set", key, value] and the exit code
// propagates as-is.
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

// RF-state — reset confirmation: only ["reset", key] is passed, without a value.
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

// RF-state — default Run delegation (deps.Run nil): the real ops.RunTuning set standard path is
// reused — the one-line JSON output ({"status":"set",...}), real-path value
// validation, and the meta write all belong to the existing path (proving no duplicate implementation).
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
	// invalid values also exit 3 via standard-path validation (real-path parse-fallback rejection and guidance).
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

// RF-protocol — ctrl+c cancel (huh.ErrUserAborted): after the one-line "tuning cancelled"
// notice, exit 0 with no run delegation — a clean cancel.
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

// RF-protocol — key-row load failure: ends via the standard error path (ErrLine + MapExit)
// without the form.
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

// TestTuningTUIPtyRealRun — one real pty run: in a real terminal the default huh form
// renders (terminal entry passes) and the ctrl+c exit is hygienically handled with the "tuning cancelled"
// notice and exit 0. The child process (FLINT_TUNING_TUI_PTY_CHILD=1)
// runs tui.RunTuningTUI with the real form and faked Rows/Run; the parent drives it via
// `script` (a BSD pty wrapper) and verifies the output and exit status. Environments without
// script skip (the real pty run is limited to this one).
func TestTuningTUIPtyRealRun(t *testing.T) {
	// the real pty run is the local pre-release TUI standard check — CI headless runners'
	// terminal-emulation timing differences make regression verdicts flaky, so CI skips it.
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
	// script's pty size is inherited from the parent tty, but go test stdout is a pipe, so it becomes
	// 0x0 (width-0 render -> empty fields). Use sh -c to set the pty size and then run the child,
	// creating the same conditions as a real terminal.
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
	var out lockedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pty child: %v", err)
	}
	// wait for render — poll until the form title ("Tuning") and the key row ("dead_days") are visible.
	// bubbletea sends terminal queries before the first paint (OSC 11 background color, DSR cursor
	// position) and waits for replies — script's pty has no responder, so the parent replies
	// like a real terminal (minimal emulator behavior).
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
	// ctrl+c (0x03) -> huh abort -> notice and exit 0 hygiene.
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

// lockedBuffer — a mutex-protected buffer for collecting pty output, accessed concurrently by the
// exec copy goroutine (Write) and the main polling loop (String). bytes.Buffer does not define
// concurrent Write/Read (a real race under go test -race — both shared-buffer Write and String
// are serialized with Lock).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tailFor — the output tail for diagnostics (last n bytes).
func tailFor(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
