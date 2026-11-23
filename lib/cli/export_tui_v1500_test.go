package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/tui"
)

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
