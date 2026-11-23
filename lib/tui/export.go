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

type ExportSelection struct {
	Format string
	Status string
	Out    string
}

type ExportPreview struct {
	Rows  int
	Bytes int
}

type ExportDeps struct {
	Preview func(ctx context.Context, sel ExportSelection) (ExportPreview, error)

	MaxRows func(ctx context.Context) int

	Run func(args []string) int

	CancelNote io.Writer
}

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
			Description(nonInteractiveHint),
	).Run()
}

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

func ExportArgs(sel ExportSelection) []string {
	args := []string{"--format", sel.Format, "--status", sel.Status}
	if sel.Out != "" {
		args = append(args, "--out", sel.Out)
	}
	return args
}

func exportCancelNotice(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, "export cancelled")
}

func RunExport(ctx context.Context, deps ExportDeps) (int, error) {
	if !isTerminal() {

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
