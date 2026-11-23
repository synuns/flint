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

func pendingDemoStore() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{7, "demo topic alpha", "demo summary alpha", "hook", "2026-11-14T10:00:00Z", "0.85"}}, nil
		}
		return nil, nil
	}}
}

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

func installPendingTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := pendingTTYFn
	pendingTTYFn = func() bool { return tty }
	t.Cleanup(func() { pendingTTYFn = orig })
}

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
