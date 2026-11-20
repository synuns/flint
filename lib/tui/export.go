// export.go — the `flint export --tui` interactive flow.
// huh form (5 formats, status, output path) -> preview (row count, estimated
// bytes) -> execution reuses the existing flag path (cli.runExport) by passing
// the assembled arguments (no duplicate implementation — the output, sidecar, and
// row cap all apply automatically per the existing path's contract). The query,
// formatters, cap, and execution dependencies are injected from the cli side (both avoiding the import cycle and serving as seams — tests inject fakes via the form/confirm stage vars).
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/wire"
)

// ExportSelection — the huh form result (format, status, output path). An empty
// Out means stdout output (stdout mode).
type ExportSelection struct {
	Format string
	Status string
	Out    string
}

// ExportPreview — the preview result: the selected filter's row count and estimated bytes.
type ExportPreview struct {
	Rows  int
	Bytes int
}

// ExportDeps — the cli-injected dependencies. Preview, MaxRows, and Run are all
// wrappers of the existing query, registry, and execution seams (the TUI does not reimplement source logic).
type ExportDeps struct {
	// Preview — computes the row count and estimated bytes for the selected filter
	// (reusing the existing query and formatter seams). If nil, the preview and confirm stages are skipped.
	Preview func(ctx context.Context, sel ExportSelection) (ExportPreview, error)
	// MaxRows — the row cap (the registry key export_import_max_rows, including the
	// default fallback). If nil, the cap check is skipped (the existing path
	// re-checks at execution).
	MaxRows func(ctx context.Context) int
	// Run — runs the existing runExport with the assembled arguments. Returns the exit code.
	Run func(args []string) int
	// CancelNote — where to write the 1-line "export cancelled" notice on a user
	// cancel (form abort or confirmation decline). If nil, os.Stdout.
	CancelNote io.Writer
}

// exportFormFn — the huh form seam (test fake injection). The default form: 5
// formats, 2 statuses, and the output path (optional — empty means stdout). ctrl+c is huh.ErrUserAborted.
var exportFormFn = func(sel *ExportSelection) error {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Format").
				Options(huh.NewOptions("jsonl", "md", "txt", "csv", "pam")...).
				Value(&sel.Format),
			huh.NewSelect[string]().
				Title("Status").
				Options(huh.NewOptions("active", "all")...).
				Value(&sel.Status),
			huh.NewInput().
				Title("Output file (optional — empty writes to stdout)").
				Value(&sel.Out),
		).
			// the non-interactive escape guidance (the adaptive default-entry notice) — the bottom of the form.
			Description(nonInteractiveHint),
	).Run()
}

// exportConfirmFn — the preview-confirmation seam (test fake injection). The default
// is a huh confirm form: shows the row count and estimated bytes and asks whether to proceed.
var exportConfirmFn = func(pv ExportPreview) (bool, error) {
	proceed := false
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Export %d rows (~%d bytes)?", pv.Rows, pv.Bytes)).
				Description("The existing flag path applies the row limit, checksum sidecar, and summary as-is.").
				Affirmative("Export").
				Negative("Cancel").
				Value(&proceed),
		),
	).Run()
	return proceed, err
}

// ExportArgs — assembles the form result -> flag arguments (a single source of
// truth — the Run delegation uses this same assembly). An empty Out adds no
// --out (stdout mode).
func ExportArgs(sel ExportSelection) []string {
	args := []string{"--format", sel.Format, "--status", sel.Status}
	if sel.Out != "" {
		args = append(args, "--out", sel.Out)
	}
	return args
}

// exportCancelNotice — the 1-line user-cancel notice (abolishing the silent
// 0 bytes — A3). exit 0 is kept.
func exportCancelNotice(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, "export cancelled")
}

// RunExport — the export TUI flow: form -> preview (cap check) -> confirmation
// -> the existing flag path. Returns (exitCode, err): if err != nil, the caller
// falls back to 1 line of error JSON + exit 3. Non-terminal immediately returns
// (3, error) before the form starts — the convention "TUI requires a terminal".
// A user abort (ctrl+c) shows the export-cancelled notice and returns (0, nil) without executing — a clean cancel is not an error.
func RunExport(ctx context.Context, deps ExportDeps) (int, error) {
	if !isTerminal() {
		// the invalid-argument classification (error:"usage") — the same
		// contract as the Run wrapper; exit 3 unchanged.
		return 3, wire.UsageErrorf("%w", ErrTerminalRequired)
	}
	sel := ExportSelection{Format: "jsonl", Status: "active"}
	if err := exportFormFn(&sel); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			exportCancelNotice(deps.CancelNote)
			return 0, nil
		}
		return 3, fmt.Errorf("export tui form failed: %w", err)
	}
	if deps.Preview != nil {
		pv, err := deps.Preview(ctx, sel)
		if err != nil {
			return 3, fmt.Errorf("export preview failed: %w", err)
		}
		if deps.MaxRows != nil {
			if max := deps.MaxRows(ctx); pv.Rows > max {
				return 3, fmt.Errorf("export exceeds max rows: %d > %d (export_import_max_rows) — narrow the filter or raise the limit", pv.Rows, max)
			}
		}
		proceed, err := exportConfirmFn(pv)
		if err != nil && !errors.Is(err, huh.ErrUserAborted) {
			return 3, fmt.Errorf("export tui confirm failed: %w", err)
		}
		if !proceed {
			exportCancelNotice(deps.CancelNote)
			return 0, nil
		}
	}
	if deps.Run == nil {
		return 3, fmt.Errorf("export tui: run delegate not wired")
	}
	return deps.Run(ExportArgs(sel)), nil
}
