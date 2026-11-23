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

type TuningKeyRow struct {
	Key      string
	Value    string
	Default  string
	Consumer string
}

type TuningSelection struct {
	Key    string
	Value  string
	Action string
}

type TuningDeps struct {
	Rows func(ctx context.Context) ([]TuningKeyRow, error)

	Form func(sel *TuningSelection, rows []TuningKeyRow) error

	IsTerminal func() bool

	Run func(args []string) int

	CancelNote io.Writer
}

var tuningRunner = ops.RunTuning

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

var tuningFormFn = func(sel *TuningSelection, rows []TuningKeyRow) error {
	opts := make([]huh.Option[string], 0, len(rows))
	for _, r := range rows {
		opts = append(opts, huh.NewOption(
			fmt.Sprintf("%s — now %s (default %s)", r.Key, r.Value, r.Default), r.Key))
	}

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
