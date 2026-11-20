// tuning.go — entry branching for `flint tuning --tui`. Opt-in only (no adaptive
// switching — the no-arg list JSON is an agent/script contract, so it stays unchanged): --tui
// standalone only goes to the TUI (tui.RunTuningTUI); combinations are invalid-arg exit 3 (carrying over the maintain
// convention — tuning also takes the positional subcommands set/get/reset, preventing ghost-argument
// combinations). FLINT_NO_TUI is layer 1 of the 3-layer guard — even an explicit --tui is blocked, then the existing path is
// announced. Everything else delegates byte-for-byte unchanged to ops.RunTuning (pipes and CI
// unaffected). The TUI launcher and ops delegation are seam vars (test fake injection).
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// tuningTuiFn — TUI entry seam. Real implementation by default; tests swap in a fake.
var tuningTuiFn = tui.RunTuningTUI

// tuningOpsRun — delegation seam for the existing tuning path (ops.RunTuning — no duplicate
// implementation). Real implementation by default; entry-branching tests swap in a fake.
var tuningOpsRun = ops.RunTuning

func init() {
	// overwrites the ops.init registration (RunTuning) — inherits the latest wire.Register value
	// (same-name re-registration convention, the pending/maintain/report precedent). The default path
	// is unchanged via the delegation below (this file owns only the --tui branch).
	Register("tuning", runTuningEntry)
}

// runTuningEntry — tuning handler. Entry rules: (1) --tui standalone -> TUI (combinations are
// invalid-arg exit 3, blocked with exit 3 when FLINT_NO_TUI is set, non-terminal keeps the TUI
// launcher fallback exit 3 unchanged) (2) everything else (no args, all subcommands) ->
// the current ops.RunTuning path — opt-in only, so there is no no-arg + terminal adaptive switching
// (no args = list JSON unchanged).
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
		// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY
		// (the explicit agent/CI escape hatch takes precedence).
		if tui.NoTUIForced() {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"tuning: --tui is blocked by FLINT_NO_TUI — use tuning flag-mode subcommands")))
			return 3
		}
		return tuningTuiFn(ctx, st, stdout, tui.TuningDeps{})
	}
	return tuningOpsRun(ctx, st, args, stdin, stdout)
}
