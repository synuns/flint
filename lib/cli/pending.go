// pending.go — entry branching for `flint pending`. Only --tui opt-in and
// adaptive switching (no args + terminal) go to the TUI (tui.RunPendingTUI);
// everything else (flags present, no-arg pipe, unknown flag exit 3)
// delegates unchanged to knowledge.RunPending (pipes and CI unaffected). The TUI launcher and TTY
// check are seam vars (test fake injection).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// pendingTuiFn — TUI entry seam. Real implementation by default; tests swap in a fake.
var pendingTuiFn = tui.RunPendingTUI

// pendingTTYFn — TTY check seam for adaptive entry (the setupWizardTTYFn
// convention — real implementation tui.IsTerminalStdout by default; tests swap in a fake).
var pendingTTYFn = tui.IsTerminalStdout

func init() {
	// overwrites the knowledge.init registration (RunPending) — inherits the latest
	// wire.Register value (same-name re-registration convention). The default path is unchanged via the delegation below.
	Register("pending", runPendingEntry)
}

// runPendingEntry — pending handler. Entry rules:
// (1) explicit --tui -> TUI (standalone only — combinations are invalid-arg exit 3; non-terminal keeps
// the TUI-launcher fallback exit 3) (2) no args or flags at all + terminal -> TUI
// (3) no args + non-terminal (pipe) -> current RunPending output (4) flags present ->
// the current RunPending path.
func runPendingEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					"unknown flag: %s — usage: flint pending [--tui]", b)))
				return 3
			}
		}
		return pendingTuiFn(ctx, st, stdout)
	}
	// adaptive switching: no args + terminal means TUI (equivalent to --tui).
	// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY.
	if len(args) == 0 && !tui.NoTUIForced() && pendingTTYFn() {
		return pendingTuiFn(ctx, st, stdout)
	}
	return knowledge.RunPending(ctx, st, args, stdin, stdout)
}
