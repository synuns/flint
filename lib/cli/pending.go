package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

var pendingTuiFn = tui.RunPendingTUI

var pendingTTYFn = tui.IsTerminalStdout

func init() {

	Register("pending", runPendingEntry)
}

func runPendingEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					i18n.T("err.unknown_flag")+" — "+i18n.T("usage.pending"), wire.MaskToken(b))))
				return 3
			}
		}
		return pendingTuiFn(ctx, st, stdout)
	}

	if len(args) == 0 && !tui.NoTUIForced() && pendingTTYFn() {
		return pendingTuiFn(ctx, st, stdout)
	}
	return knowledge.RunPending(ctx, st, args, stdin, stdout)
}
