// export_tui_v1500_test.go — contract for the `flint export --tui` entry branch.
// The runExport body (flag parsing, output, sidecar, cap) is owned by the existing export_test.go —
// here we only verify (1) the --tui entry branch delegates to the TUI-flow seam, (2) without
// --tui the flag path is unchanged, and (3) the default wiring's non-terminal fallback.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/tui"
)

// RF1 — entry branch: `flint export --tui` delegates to exportTUIFn (tui.RunExport) instead of
// runExport flag parsing, passing the exit code through.
func TestRunExportTUIEntryBranch(t *testing.T) {
	installExportFakes(t, nil)
	called := 0
	old := exportTUIFn
	exportTUIFn = func(ctx context.Context, deps tui.ExportDeps) (int, error) {
		called++
		if deps.Run == nil {
			t.Fatal("deps.Run must be wired to the flag path")
		}
		return 7, nil
	}
	t.Cleanup(func() { exportTUIFn = old })
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--tui"}, strings.NewReader(""), &stdout)
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (TUI 위임 코드 통과)", code)
	}
	if called != 1 {
		t.Fatalf("exportTUIFn called %d times, want 1", called)
	}
}

// RF2 — flag-path regression: without --tui, the existing behavior is unchanged — the TUI seam is
// not called and the jsonl stdout + summary stderr contract is kept as-is.
func TestRunExportFlagPathUnchangedWithoutTUI(t *testing.T) {
	stderr := installExportFakes(t, nil)
	old := exportTUIFn
	exportTUIFn = func(ctx context.Context, deps tui.ExportDeps) (int, error) {
		t.Fatal("TUI flow must not run without --tui")
		return 1, nil
	}
	t.Cleanup(func() { exportTUIFn = old })
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--format", "md"}, strings.NewReader(""), &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q)", code, stdout.String())
	}
	if got, want := stdout.String(), "<md of 2 rows>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "exported 2 rows (md, ") {
		t.Fatalf("stderr summary missing: %q", stderr.String())
	}
}

// RF3 — default-wiring non-terminal fallback: in a pipe/test environment (not a terminal),
// --tui exits 3 + one line of the "TUI requires a terminal" error JSON.
func TestRunExportTUIDefaultWiringNonTerminalFallback(t *testing.T) {
	installExportFakes(t, nil)
	var stdout bytes.Buffer
	code := runExport(context.Background(), &fakeStore{}, []string{"--tui"}, strings.NewReader(""), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout %q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Fatalf("fallback message missing: %q", stdout.String())
	}
}
