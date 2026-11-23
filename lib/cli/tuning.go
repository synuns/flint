package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

var tuningTuiFn = tui.RunTuningTUI

var tuningOpsRun = ops.RunTuning

var tuningTTY = tui.IsTerminalStdout

func init() {

	Register("tuning", runTuningEntry)
}

func runTuningEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					"tuning: --tui cannot be combined with %s — run it alone", b)))
				return 3
			}
		}

		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"tuning: --tui is blocked by FLINT_NO_TUI — use tuning flag-mode subcommands")))
			return 3
		}
		return tuningTuiFn(ctx, st, stdout, tui.TuningDeps{})
	}
	if len(args) == 0 && tuningTTY() {
		return tuningTuiFn(ctx, st, stdout, tui.TuningDeps{})
	}
	return tuningOpsRun(ctx, st, args, stdin, stdout)
}
