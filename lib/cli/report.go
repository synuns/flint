// report.go — entry branching for `flint report --tui`. Registration is done first by the ops
// package init (RunReport); the cli init re-registers this wrapper to
// overwrite it (last Register wins — cli imports ops, so ordering is guaranteed).
// --tui opt-in: the existing 3 views (JSON default, --text, --html) path is delegated byte-for-byte
// unchanged. TUI data reuses the RunReport (JSON view) collection output, parsed
// as-is — all 4 views share one source (no duplicate collection implementation).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// collection and screen seams (test injection — carrying over the export convention: real
// implementation by default, tests swap in fakes, nil reassignment means default).
var (
	// reportCollectFn — collects the ReportOutput (reusing the JSON view path — just
	// receiving the output into a buffer instead of stdout). On error: (one error line, exit code).
	reportCollectFn = func(ctx context.Context, st Store, args []string, stdin io.Reader) (string, int) {
		var buf strings.Builder
		code := runReport(ctx, st, args, stdin, &buf)
		return buf.String(), code
	}
	// reportTUIFn — launches the screen (returns a non-terminal error — caller exits 3).
	reportTUIFn = tui.RunReportTUI
	// reportTTYFn — TTY check seam for adaptive entry (real implementation
	// tui.IsTerminalStdout by default; tests swap in a fake).
	reportTTYFn = tui.IsTerminalStdout
)

func init() { Register("report", runReportEntry) }

// runReportEntry — `flint report [--tui] [existing flags...]`. Entry rules:
// (1) explicit --tui -> TUI (non-terminal fallback exit 3 unchanged)
// (2) no args or flags + terminal -> TUI (3) no args + non-terminal -> default JSON view
// (4) flags present -> existing path. --tui cannot combine with view flags (--html, --text, --out)
// (exit 3 — prevents screen/file duality). TUI path: collect (JSON source) ->
// parse ReportOutput -> TUI. Window days are derived from the collection output row count (the actual
// window after the cap clamp).
func runReportEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	tuiIdx := -1
	for i, a := range args {
		if a == "--tui" {
			tuiIdx = i
			break
		}
	}
	// adaptive switching: no args or flags at all + terminal means TUI
	// (equivalent to --tui — rest is empty, so view-flag validation passes automatically).
	// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY.
	if tuiIdx < 0 && !(len(args) == 0 && !tui.NoTUIForced() && reportTTYFn()) {
		return runReport(ctx, st, args, stdin, stdout) // unchanged delegation to the default path
	}
	rest := args
	if tuiIdx >= 0 {
		rest = append(append([]string{}, args[:tuiIdx]...), args[tuiIdx+1:]...)
	}
	for _, a := range rest {
		switch a {
		case "--html", "--text", "--out":
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --tui cannot combine with %s — usage: flint report [--tui] | [--days N] [--html] [--out FILE] [--text]", a)))
			return 3
		}
	}
	// checking the token after --out is unnecessary — the combination where an --out value comes first
	// is already caught by RunReport argument validation as exit 3 (--out absorbs a value).
	line, code := reportCollectFn(ctx, st, rest, stdin)
	if code != 0 {
		fmt.Fprint(stdout, line) // forward the collection output's one error line (with newline)
		return code
	}
	var rep ops.ReportOutput
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &rep); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("report: --tui parse: %v", err)))
		return 1
	}
	run := reportTUIFn
	if run == nil {
		run = tui.RunReportTUI
	}
	if err := run(rep, len(rep.Days)); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}
