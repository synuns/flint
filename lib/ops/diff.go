// diff.go — flint diff <from> <to>: compares knowledge changes between two as-of points.
// Row details and the summary commit list come as typed values from wire.Store's DiffRows
// and DiffCommits (owned by the db implementation). The output is one-line JSON; invalid
// as-of specs exit 3. as-of normalization is cli-owned asof.go — injected via the
// ParseAsOfSpecFn seam.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseAsOfSpecFn — the as-of spec normalization seam. The real implementation,
// wire.ParseAsOfSpec, is injected by the cli/root.go composition root init.
var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

// ErrInvalidAsOf — an invalid as-of spec (format error, prefix ambiguity/not found, or
// before the first commit). Mapped to exit 3 at the command layer. The same value as
// wire.ErrInvalidAsOf (injected by the root.go composition root) — errors.Is checks unchanged.
var ErrInvalidAsOf error

// diffRow — one row detail. id and topic use the to_* values for added and modified,
// from_* for removed (observed: removed rows have to_* NULL -> COALESCE).
type diffRow struct {
	DiffType string `json:"diff_type"`
	ID       any    `json:"id"`
	Topic    any    `json:"topic"`
}

// diffCommit — one summary commit. data_change and schema_change are the OR over all
// that commit's tables (dolt_diff's per-table flag aggregation).
type diffCommit struct {
	Hash         string    `json:"hash"`
	Date         time.Time `json:"date"`
	Message      string    `json:"message"`
	DataChange   bool      `json:"data_change"`
	SchemaChange bool      `json:"schema_change"`
}

// diffCounts — the summary counts (exactly the --summary output).
type diffCounts struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Commits  []diffCommit `json:"commits"`
}

// diffDetail — the default-mode output (counts + row details).
type diffDetail struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Rows     []diffRow    `json:"rows"`
	Commits  []diffCommit `json:"commits"`
}

// RunDiff — `flint diff <from> <to> [--summary] [--data|--schema]`.
// from and to are as-of specs (each normalized to a commit hash via ParseAsOfSpec). --data
// and --schema filter the commit summary to commits with those changes.
func RunDiff(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	summary, dataOnly, schemaOnly := false, false, false
	var pos []string
	for _, a := range args {
		switch a {
		case "--summary":
			summary = true
		case "--data":
			dataOnly = true
		case "--schema":
			schemaOnly = true
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		// not a raw string JSON but wire.UsageErrorf -> the ErrLine
		// path (error:"usage").
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"usage: flint diff <from> <to> [--summary] [--data|--schema]")))
		return 3
	}
	from, err := ParseAsOfSpecFn(ctx, st, pos[0])
	if err != nil {
		return diffFail(stdout, err)
	}
	to, err := ParseAsOfSpecFn(ctx, st, pos[1])
	if err != nil {
		return diffFail(stdout, err)
	}

	// row details — DiffRows returns the added(to_*)/removed(from_*) COALESCE expression as
	// typed values (owned by the db implementation).
	rows, err := st.DiffRows(ctx, from, to)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	counts := diffCounts{From: from, To: to, Commits: []diffCommit{}}
	detail := make([]diffRow, 0, len(rows))
	for _, r := range rows {
		switch r.DiffType {
		case "added":
			counts.Added++
		case "modified":
			counts.Modified++
		case "removed":
			counts.Removed++
		}
		detail = append(detail, diffRow{
			DiffType: r.DiffType,
			ID:       r.ID,
			Topic:    render.Text(r.Topic), // via render.Text (inheriting output re-sanitization)
		})
	}

	// the summary commit list — newest first (the cap is on the implementation side). --data and --schema narrow it here.
	commits, err := st.DiffCommits(ctx, from, to)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	for _, s := range commits {
		c := diffCommit{
			Hash:         s.Hash,
			Date:         s.Date,
			Message:      render.Text(s.Message), // via render.Text
			DataChange:   s.DataChange,
			SchemaChange: s.SchemaChange,
		}
		if dataOnly && !c.DataChange {
			continue
		}
		if schemaOnly && !c.SchemaChange {
			continue
		}
		counts.Commits = append(counts.Commits, c)
	}

	var out any = counts
	if !summary {
		out = diffDetail{
			From: counts.From, To: counts.To,
			Added: counts.Added, Modified: counts.Modified, Removed: counts.Removed,
			Rows: detail, Commits: counts.Commits,
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// diffFail — the as-of normalization failure output. ErrInvalidAsOf is an invalid_as_of
// one-line JSON + exit 3; other query errors keep the existing errLine/MapExit path.
func diffFail(stdout io.Writer, err error) int {
	if errors.Is(err, ErrInvalidAsOf) {
		b, _ := json.Marshal(map[string]string{"error": "invalid_as_of", "message": err.Error()})
		fmt.Fprintln(stdout, string(b))
		return 3
	}
	fmt.Fprintln(stdout, wire.ErrLine(err))
	return store.MapExit(err)
}
