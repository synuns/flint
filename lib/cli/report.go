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

var (
	reportCollectFn = func(ctx context.Context, st Store, args []string, stdin io.Reader) (string, int) {
		var buf strings.Builder
		code := runReport(ctx, st, args, stdin, &buf)
		return buf.String(), code
	}

	reportTUIFn = tui.RunReportTUI

	reportTTYFn = tui.IsTerminalStdout
)

func init() { Register("report", runReportEntry) }

func runReportEntry(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	tuiIdx := -1
	for i, a := range args {
		if a == "--tui" {
			tuiIdx = i
			break
		}
	}

	if tuiIdx < 0 && !(len(args) == 0 && !tui.NoTUIForced() && reportTTYFn()) {
		return runReport(ctx, st, args, stdin, stdout)
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

	line, code := reportCollectFn(ctx, st, rest, stdin)
	if code != 0 {
		fmt.Fprint(stdout, line)
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
