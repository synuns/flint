// maintain.go — entry branching for `flint maintain --tui`. Opt-in only
// (no adaptive switching — the maintain subcommand takes many arguments, which prevents ghost-argument
// regressions): --tui standalone only goes to the TUI (tui.RunMaintainTUI); combinations are invalid-arg
// exit 3. FLINT_NO_TUI is layer 1 of the 3-layer guard — even an explicit --tui is blocked, then the existing path is
// announced. Everything else delegates unchanged to ops.RunMaintain (pipes and CI unaffected).
// The TUI launcher and ops delegation are seam vars (test fake injection).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// maintainTuiFn — TUI entry seam. Real implementation by default; tests swap in a fake.
var maintainTuiFn = tui.RunMaintainTUI

// maintainOpsRun — delegation seam for the existing maintain path (ops.RunMaintain — no duplicate
// implementation). Real implementation by default; entry-branching tests swap in a fake.
var maintainOpsRun = ops.RunMaintain

func init() {
	// overwrites the ops.init registration (RunMaintain) — inherits the latest wire.Register value
	// (same-name re-registration convention, the pending precedent). The default path is unchanged via
	// the delegation below (this file owns only the --tui branch).
	Register("maintain", runMaintainEntry)
}

// runMaintainEntry — maintain handler. Entry rules: (1) --tui standalone -> TUI
// (combinations are invalid-arg exit 3; blocked with exit 3 when FLINT_NO_TUI is set) (2) everything
// else (no args, any flags) -> the current ops.RunMaintain path — opt-in only, so there is no
// no-arg + terminal adaptive switching (unlike pending and export, to prevent ghost
// arguments).
func runMaintainEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	for i, a := range args {
		if a != "--tui" {
			continue
		}
		for j, b := range args {
			if j != i && b != "--tui" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
					"maintain: --tui cannot be combined with %s — run it alone", b)))
				return 3
			}
		}
		// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY
		// (the explicit agent/CI escape hatch takes precedence).
		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"maintain: --tui is blocked by FLINT_NO_TUI — use maintain flag-mode subcommands")))
			return 3
		}
		return maintainTuiFn(ctx, st, stdout)
	}
	return maintainOpsRun(ctx, st, args, stdin, stdout)
}
