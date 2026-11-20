// export_test.go — contract tests for the `flint export --tui` flow.
// The huh form, confirm stage, and terminal check are faked via seam vars — the
// verification targets are (1) the form result -> flag-argument assembly, (2) the
// preview row-count and byte delegation, (3) the over-cap error, and (4) the
// delegation, (3) the over-cap error, and (4) the execution delegation (reusing RunExport — no duplicate implementation). The real form wiring activates at the seam defaults (compile time).
package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// overrideExportTUISeams — bulk-swaps the export TUI seams (terminal check, form, confirm).
// If form or confirm is nil, the default seam is kept (only the terminal check is swapped).
func overrideExportTUISeams(t *testing.T, term bool, form func(*ExportSelection) error, confirm func(ExportPreview) (bool, error)) {
	t.Helper()
	oldTerm, oldForm, oldConfirm := isTerminal, exportFormFn, exportConfirmFn
	isTerminal = func() bool { return term }
	if form != nil {
		exportFormFn = form
	}
	if confirm != nil {
		exportConfirmFn = confirm
	}
	t.Cleanup(func() { isTerminal, exportFormFn, exportConfirmFn = oldTerm, oldForm, oldConfirm })
}

// RF1 — the form result -> argument assembly: --format/--status/--out assembled
// in the format, status, out order. An empty Out (stdout) omits --out.
func TestExportArgsAssemblesFormSelection(t *testing.T) {
	got := ExportArgs(ExportSelection{Format: "md", Status: "all", Out: "/tmp/export.md"})
	want := []string{"--format", "md", "--status", "all", "--out", "/tmp/export.md"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
	// stdout mode (no Out): --out not included.
	got = ExportArgs(ExportSelection{Format: "jsonl", Status: "active"})
	for _, a := range got {
		if a == "--out" {
			t.Fatalf("empty Out must omit --out: %v", got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("args = %v, want 4 elements [--format f --status s]", got)
	}
}

// RF2 — the form selection passes identically to the preview and execution:
// Preview returns the row count and bytes for the selected filter, and Run
// re-runs the existing flag path (runExport) with the assembled arguments (the exit code passes through).
func TestRunExportFormSelectionFlowsToPreviewAndRun(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error {
			sel.Format, sel.Status, sel.Out = "csv", "all", "/tmp/demo.csv"
			return nil
		},
		func(pv ExportPreview) (bool, error) { return true, nil })
	var gotSel ExportSelection
	var gotArgs []string
	ran := 0
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			gotSel = sel
			return ExportPreview{Rows: 3, Bytes: 120}, nil
		},
		MaxRows: func(ctx context.Context) int { return 100 },
		Run: func(args []string) int {
			ran++
			gotArgs = args
			return 0
		},
	}
	code, err := RunExport(context.Background(), deps)
	if err != nil || code != 0 {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if ran != 1 {
		t.Fatalf("Run called %d times, want 1", ran)
	}
	if gotSel.Format != "csv" || gotSel.Status != "all" || gotSel.Out != "/tmp/demo.csv" {
		t.Fatalf("selection not propagated to preview: %+v", gotSel)
	}
	want := []string{"--format", "csv", "--status", "all", "--out", "/tmp/demo.csv"}
	if len(gotArgs) != len(want) {
		t.Fatalf("Run args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("Run args = %v, want %v", gotArgs, want)
		}
	}
}

// Non-terminal (pipe, CI): falls back immediately to (3, error) before the form
// starts — the convention "TUI requires a terminal". The form never opens.
func TestRunExportRequiresTerminal(t *testing.T) {
	overrideExportTUISeams(t, false,
		func(sel *ExportSelection) error { t.Fatal("form must not open without a terminal"); return nil },
		nil)
	deps := ExportDeps{Run: func(args []string) int { t.Fatal("Run must not execute"); return 1 }}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
	if !strings.Contains(err.Error(), "TUI requires a terminal") {
		t.Fatalf("error must name the terminal requirement: %v", err)
	}
}

// RF4 — a user abort (ctrl+c, huh.ErrUserAborted): after the "export cancelled"
// notice, (0, nil) without executing — a clean cancel is not an error (the notice is RF4a).
func TestRunExportUserAbortExitsCleanly(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { return huh.ErrUserAborted },
		nil)
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			t.Fatal("preview must not run after abort")
			return ExportPreview{}, nil
		},
		Run: func(args []string) int { t.Fatal("Run must not execute after abort"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
}

// RF4a — A3: both a form abort and a confirmation decline produce not a silent
// 0 bytes but a 1-line "export cancelled" notice (CancelNote — default os.Stdout) and exit 0.
func TestRunExportCancelPrintsNotice(t *testing.T) {
	t.Run("form abort", func(t *testing.T) {
		var out bytes.Buffer
		overrideExportTUISeams(t, true,
			func(sel *ExportSelection) error { return huh.ErrUserAborted },
			nil)
		deps := ExportDeps{
			CancelNote: &out,
			Run:        func(args []string) int { t.Fatal("Run must not execute"); return 1 },
		}
		code, err := RunExport(context.Background(), deps)
		if code != 0 || err != nil {
			t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
		}
		if !strings.Contains(out.String(), "export cancelled") {
			t.Fatalf("stdout = %q, want 'export cancelled' notice", out.String())
		}
	})
	t.Run("confirm declined", func(t *testing.T) {
		var out bytes.Buffer
		overrideExportTUISeams(t, true,
			func(sel *ExportSelection) error { sel.Format, sel.Status = "md", "active"; return nil },
			func(pv ExportPreview) (bool, error) { return false, nil })
		deps := ExportDeps{
			CancelNote: &out,
			Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
				return ExportPreview{Rows: 1, Bytes: 10}, nil
			},
			MaxRows: func(ctx context.Context) int { return 100 },
			Run:     func(args []string) int { t.Fatal("Run must not execute when declined"); return 1 },
		}
		code, err := RunExport(context.Background(), deps)
		if code != 0 || err != nil {
			t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
		}
		if !strings.Contains(out.String(), "export cancelled") {
			t.Fatalf("stdout = %q, want 'export cancelled' notice", out.String())
		}
	})
}

// RF5 — over-cap TUI error: if the preview row count > export_import_max_rows,
// it becomes (3, err) instead of the confirm stage — Run is never called.
func TestRunExportOverLimitShowsTUIError(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "jsonl", "all"; return nil },
		func(pv ExportPreview) (bool, error) {
			t.Fatal("confirm must not run over the limit")
			return false, nil
		})
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{Rows: 3, Bytes: 90}, nil
		},
		MaxRows: func(ctx context.Context) int { return 2 },
		Run:     func(args []string) int { t.Fatal("Run must not execute over the limit"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "max rows") || !strings.Contains(msg, "export_import_max_rows") {
		t.Fatalf("error must name the limit and its key: %v", msg)
	}
}

// RF6 — declining the preview confirmation: (0, nil) without executing.
func TestRunExportDeclineSkipsRun(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "md", "active"; return nil },
		func(pv ExportPreview) (bool, error) { return false, nil })
	var previewed ExportPreview
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			previewed = ExportPreview{Rows: 2, Bytes: 64}
			return previewed, nil
		},
		MaxRows: func(ctx context.Context) int { return 100 },
		Run:     func(args []string) int { t.Fatal("Run must not execute when declined"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if previewed.Rows != 2 {
		t.Fatalf("preview must run before confirm: %+v", previewed)
	}
}

// RF7 — a preview query failure: (3, err), no execution.
func TestRunExportPreviewFailureExits3(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { return nil },
		nil)
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{}, context.DeadlineExceeded
		},
		Run: func(args []string) int { t.Fatal("Run must not execute on preview failure"); return 1 },
	}
	code, err := RunExport(context.Background(), deps)
	if code != 3 || err == nil {
		t.Fatalf("RunExport = (%d, %v), want (3, error)", code, err)
	}
}

// RF8 — the boundary (row count == max): not over — proceeds to the confirm stage.
func TestRunExportBoundaryEqualMaxProceeds(t *testing.T) {
	overrideExportTUISeams(t, true,
		func(sel *ExportSelection) error { sel.Format, sel.Status = "jsonl", "active"; return nil },
		func(pv ExportPreview) (bool, error) { return true, nil })
	ran := 0
	deps := ExportDeps{
		Preview: func(ctx context.Context, sel ExportSelection) (ExportPreview, error) {
			return ExportPreview{Rows: 2, Bytes: 48}, nil
		},
		MaxRows: func(ctx context.Context) int { return 2 },
		Run: func(args []string) int {
			ran++
			return 0
		},
	}
	code, err := RunExport(context.Background(), deps)
	if code != 0 || err != nil {
		t.Fatalf("RunExport = (%d, %v), want (0, nil)", code, err)
	}
	if ran != 1 {
		t.Fatalf("boundary ==max must proceed to run, ran=%d", ran)
	}
}
