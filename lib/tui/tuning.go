// tuning.go — the `flint tuning --tui` interactive form (huh). Key selection ->
// value editing -> set/reset confirmation. The key list comes from the TuningKeys
// single source (the ops registry); value validation (loadThresholds ->
// ParseThresholds real path — rejecting fallback values with guidance) and the
// 1-line JSON output delegate as-is to the existing ops.RunTuning set/reset
// standard path (no duplicate implementation). Non-terminal --tui exits 3 immediately
// before the form starts (the ErrTerminalRequired convention). The entry branching (opt-in only, FLINT_NO_TUI blocking) is owned by lib/cli/tuning.go — here only the TUI flow.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// TuningKeyRow — a form key-selection row (current and default values shown).
// Derived from the TuningKeys single source.
type TuningKeyRow struct {
	Key      string
	Value    string
	Default  string
	Consumer string
}

// TuningSelection — the huh form confirmation result. Action "set" uses
// Value; "reset" restores the default (Value unused).
type TuningSelection struct {
	Key    string
	Value  string
	Action string
}

// TuningDeps — the dependencies injected by the entry branch (cli) and tests (the
// export ExportDeps convention). nil fields resolve to the default real implementations.
type TuningDeps struct {
	// Rows — loads the key rows. Default: the ops.TuningKeys x ops.TuningCurrent real paths.
	Rows func(ctx context.Context) ([]TuningKeyRow, error)
	// Form — the huh form. Default: key selection -> value editing -> a single set/reset-choice group.
	Form func(sel *TuningSelection, rows []TuningKeyRow) error
	// IsTerminal — the terminal check. Default: the tui-internal check (the isTerminal seam).
	// The FLINT_NO_TUI check is already owned by cli's 3-layer defense layer 1 (uses the raw check).
	IsTerminal func() bool
	// Run — delegates to the existing set/reset standard path (default ops.RunTuning
	// — the 1-line JSON output, real-path value validation, and exit codes all reuse the standard path).
	Run func(args []string) int
	// CancelNote — where to write the user-cancel (ctrl+c) 1-line notice. Default:
	// the caller's stdout (the post-TUI channel — same as os.Stdout in the real implementation).
	CancelNote io.Writer
}

// tuningRunner — the seam sourcing the default Run delegation (ops.RunTuning — no
// duplicate implementation). When deps.Run is nil, calls it with st and stdout.
var tuningRunner = ops.RunTuning

// tuningKeyRows — the default key-row load: combines the TuningKeys single source
// with the current values (TuningCurrent — the loadThresholds -> ParseThresholds
// real-path product), deterministically sorted (the same convention as the list display).
func tuningKeyRows(ctx context.Context, st wire.Store) ([]TuningKeyRow, error) {
	th, _, err := ops.TuningCurrent(ctx, st)
	if err != nil {
		return nil, err
	}
	keys := ops.TuningKeys()
	rows := make([]TuningKeyRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, TuningKeyRow{
			Key: k.Key, Value: k.Get(th), Default: k.Default, Consumer: k.Consumer,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// tuningFormFn — the default huh form: key selection (row labels show current and
// default) -> value editing (prefilled with the selected key's current value) ->
// set/reset choice. Value validation is owned not by the form but by the standard
// path at confirmation (the tuningSet real-path parse) — no dual maintenance of form validation rules (no duplicate implementation). ctrl+c is huh.ErrUserAborted.
// huh.ErrUserAborted.
var tuningFormFn = func(sel *TuningSelection, rows []TuningKeyRow) error {
	opts := make([]huh.Option[string], 0, len(rows))
	for _, r := range rows {
		opts = append(opts, huh.NewOption(
			fmt.Sprintf("%s — now %s (default %s)", r.Key, r.Value, r.Default), r.Key))
	}
	// prefill: the first key's current value and default action (the value updates via editing when the selection changes).
	if sel.Key == "" && len(rows) > 0 {
		sel.Key, sel.Value = rows[0].Key, rows[0].Value
	}
	if sel.Action == "" {
		sel.Action = "set"
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Key").Options(opts...).Value(&sel.Key),
			huh.NewInput().Title("Value (edit the current value)").Value(&sel.Value),
			huh.NewSelect[string]().Title("Action").
				Options(huh.NewOptions("set", "reset")...).Value(&sel.Action),
		).Title("Tuning").Description(nonInteractiveHint),
	).Run()
}

// RunTuningTUI — the `flint tuning --tui` entry. Non-terminal immediately
// returns ErrLine(ErrTerminalRequired) + exit 3 before the form starts (the Run
// wrapper convention — the error classification error:"usage"). A key-row load
// failure also takes the standard error path (ErrLine + MapExit). Form
// confirmation delegates to deps.Run (default: ops.RunTuning set/reset) — the JSON
// 1-line output, real-path value validation (rejecting and guiding on fallback), and
// exit codes all reuse the standard path. A user abort (ctrl+c, huh.ErrUserAborted) shows the 1-line
func RunTuningTUI(ctx context.Context, st wire.Store, stdout io.Writer, deps TuningDeps) int {
	term := isTerminal
	if deps.IsTerminal != nil {
		term = deps.IsTerminal
	}
	if !term() {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%w", ErrTerminalRequired)))
		return 3
	}
	loadRows := deps.Rows
	if loadRows == nil {
		loadRows = func(ctx context.Context) ([]TuningKeyRow, error) {
			return tuningKeyRows(ctx, st)
		}
	}
	rows, err := loadRows(ctx)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("tuning: no keys registered")))
		return 1
	}
	form := deps.Form
	if form == nil {
		form = tuningFormFn
	}
	sel := TuningSelection{}
	if err := form(&sel, rows); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			w := deps.CancelNote
			if w == nil {
				w = stdout
			}
			fmt.Fprintln(w, "tuning cancelled")
			return 0
		}
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("tuning tui form failed: %w", err)))
		return 3
	}
	run := deps.Run
	if run == nil {
		so, stc := stdout, st
		run = func(args []string) int {
			return tuningRunner(ctx, stc, args, strings.NewReader(""), so)
		}
	}
	var args []string
	if sel.Action == "reset" {
		args = []string{"reset", sel.Key}
	} else {
		args = []string{"set", sel.Key, sel.Value}
	}
	return run(args)
}
