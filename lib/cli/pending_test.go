// pending_test.go — contract tests for the `flint pending --tui` entry branching.
// The default paths (no flags, --json, unknown flag) delegate fully unchanged to knowledge.RunPending
// (regression); only --tui branches to the TUI seam. The TUI launcher is a seam var with a fake
// injected (default real = tui.RunPendingTUI — with SUPERLITE_REAL=1 +
// FLINT_TEST_DSN, a withFake-delegating fake takes the real-DB path).
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// installPendingTuiFake — swaps the pendingTuiFn seam (call-recording fake).
func installPendingTuiFake(t *testing.T) *[]wire.Store {
	t.Helper()
	var calls []wire.Store
	orig := pendingTuiFn
	pendingTuiFn = func(ctx context.Context, st wire.Store, stdout io.Writer) int {
		calls = append(calls, st)
		return 0
	}
	t.Cleanup(func() { pendingTuiFn = orig })
	return &calls
}

// pendingDemoStore — a fake serving 1 pending row (pending query + active query).
// The pending row matches the real TUI query's 6 columns (id, topic, summary, origin, valid_at,
// confidence) — for verifying the real RunPendingTUI fallback path.
func pendingDemoStore() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{7, "demo topic alpha", "demo summary alpha", "hook", "2026-11-14T10:00:00Z", "0.85"}}, nil
		}
		return nil, nil
	}}
}

// RF-protocol — default-path regression: no flags is byte-identical to RunPending.
func TestPendingEntryDefaultDelegatesUnchanged(t *testing.T) {
	var mine, ref bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &mine)
	wantCode := knowledge.RunPending(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &ref)
	if code != wantCode {
		t.Fatalf("exit = %d, want %d", code, wantCode)
	}
	if mine.String() != ref.String() {
		t.Fatalf("default path drifted:\ngot:  %q\nwant: %q", mine.String(), ref.String())
	}
}

// RF-protocol — existing-flag regression: --json takes the RunPending path (machine output),
// an unknown flag keeps the existing exit 3. Without --tui, the TUI seam is not called.
func TestPendingEntryJSONAndUnknownFlagRegression(t *testing.T) {
	calls := installPendingTuiFake(t)

	var out bytes.Buffer
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("--json exit = %d, want 0 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("--json output = %q, want machine JSON", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("--json must not reach TUI seam, calls = %d", len(*calls))
	}

	out.Reset()
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--bogus"},
		strings.NewReader(""), &out); code != 3 {
		t.Fatalf("unknown flag exit = %d, want 3", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("unknown flag must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — --tui branch: TUI seam called, exit propagates, store passed through.
func TestPendingEntryTUIBranch(t *testing.T) {
	calls := installPendingTuiFake(t)
	st := pendingDemoStore()
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), st, []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("--tui exit = %d, want seam-returned 0", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("TUI seam calls = %d, want 1", len(*calls))
	}
}

// RF-protocol — --tui + other flags: invalid-arg exit 3 (no partial combinations).
func TestPendingEntryTUIRejectsExtraFlags(t *testing.T) {
	calls := installPendingTuiFake(t)
	var out bytes.Buffer
	if code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--tui", "--json"},
		strings.NewReader(""), &out); code != 3 {
		t.Fatalf("--tui --json exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "unknown flag") {
		t.Fatalf("output = %q, want unknown flag error", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected combo must not reach TUI seam, calls = %d", len(*calls))
	}
}

// installPendingTTY — swaps the adaptive-entry TTY-check seam
// (the setupWizardTTYFn convention — default real implementation tui.IsTerminalStdout).
func installPendingTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := pendingTTYFn
	pendingTTYFn = func() bool { return tty }
	t.Cleanup(func() { pendingTTYFn = orig })
}

// RF-adaptive-2 — no args or flags + terminal: TUI delegation called.
func TestPendingEntryAdaptiveNoArgsTTYLaunchesTUI(t *testing.T) {
	calls := installPendingTuiFake(t)
	installPendingTTY(t, true)
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("TUI seam calls = %d, want 1", len(*calls))
	}
}

// RF-adaptive-3 — no args + non-terminal (pipe): byte-identical delegation to RunPending.
func TestPendingEntryAdaptiveNoArgsPipedUnchanged(t *testing.T) {
	calls := installPendingTuiFake(t)
	installPendingTTY(t, false)
	var mine, ref bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &mine)
	wantCode := knowledge.RunPending(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &ref)
	if code != wantCode {
		t.Fatalf("exit = %d, want %d", code, wantCode)
	}
	if mine.String() != ref.String() {
		t.Fatalf("piped path drifted:\ngot:  %q\nwant: %q", mine.String(), ref.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("piped run must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-adaptive-1 — explicit --tui + non-terminal: the existing fallback exit 3 unchanged
// (pendingTuiFn default = tui.RunPendingTUI — the Run-wrapper fallback).
func TestPendingEntryExplicitTUINonTerminalFallback(t *testing.T) {
	installPendingTTY(t, false)
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "TUI requires a terminal") {
		t.Fatalf("fallback message missing: %q", out.String())
	}
	if !strings.Contains(out.String(), `"error":"usage"`) {
		t.Fatalf("--tui 비터미널은 error:usage 여야 한다: %q", out.String())
	}
}

// RF-adaptive-4 — flags present + terminal: the current flag path is kept (no TUI entry).
func TestPendingEntryAdaptiveFlagWithTTYStaysFlagPath(t *testing.T) {
	calls := installPendingTuiFake(t)
	installPendingTTY(t, true)
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--json"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("flag path output = %q, want machine JSON", out.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("flag + TTY must not reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-protocol — registry: "pending" is finally registered to this entry-branching handler
// (overwriting the knowledge.init registration — inheriting the latest wire.Register value).
func TestPendingEntryRegistered(t *testing.T) {
	calls := installPendingTuiFake(t)
	h, ok := wire.Lookup("pending")
	if !ok {
		t.Fatal("pending not registered")
	}
	var out bytes.Buffer
	if code := h(context.Background(), pendingDemoStore(), []string{"--tui"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("registry dispatch exit = %d, want 0", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("registry dispatch must reach TUI seam, calls = %d", len(*calls))
	}
}

// RF-noenv — FLINT_NO_TUI=1 forces the current path even on a terminal (escape hatch —
// layer 1 of the 3-layer guard, where the environment check precedes the TTY seam).
func TestPendingEntryNoTUIEnvForcesFlagPath(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	calls := installPendingTuiFake(t)
	installPendingTTY(t, true)
	var mine, ref bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &mine)
	wantCode := knowledge.RunPending(context.Background(), pendingDemoStore(), nil,
		strings.NewReader(""), &ref)
	if code != wantCode {
		t.Fatalf("exit = %d, want %d", code, wantCode)
	}
	if mine.String() != ref.String() {
		t.Fatalf("env path drifted:\ngot:  %q\nwant: %q", mine.String(), ref.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("FLINT_NO_TUI must block TUI entry, calls = %d", len(*calls))
	}
}

// RF-noenv-explicit — FLINT_NO_TUI blocks only adaptive entry: an explicit --tui takes
// precedence over the environment variable (the cli.md contract — the user's explicit intent wins).
func TestPendingEntryExplicitTUIOverridesNoTUIEnv(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	calls := installPendingTuiFake(t)
	installPendingTTY(t, true)
	var out bytes.Buffer
	code := runPendingEntry(context.Background(), pendingDemoStore(), []string{"--tui"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out: %s)", code, out.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("explicit --tui must reach TUI seam under FLINT_NO_TUI, calls = %d", len(*calls))
	}
}
