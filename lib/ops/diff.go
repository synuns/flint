package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

var ErrInvalidAsOf error

type diffRow struct {
	DiffType string `json:"diff_type"`
	ID       any    `json:"id"`
	Topic    any    `json:"topic"`
}

type diffCommit struct {
	Hash         string    `json:"hash"`
	Date         time.Time `json:"date"`
	Message      string    `json:"message"`
	DataChange   bool      `json:"data_change"`
	SchemaChange bool      `json:"schema_change"`
}

type diffCounts struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Commits  []diffCommit `json:"commits"`
}

type diffDetail struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Rows     []diffRow    `json:"rows"`
	Commits  []diffCommit `json:"commits"`
}

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

		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"%s", i18n.T("usage.diff"))))
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
			Topic:    render.Text(r.Topic),
		})
	}

	commits, err := st.DiffCommits(ctx, from, to)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	for _, s := range commits {
		c := diffCommit{
			Hash:         s.Hash,
			Date:         s.Date,
			Message:      render.Text(s.Message),
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

func diffFail(stdout io.Writer, err error) int {
	if errors.Is(err, ErrInvalidAsOf) {
		b, _ := json.Marshal(map[string]string{"error": "invalid_as_of", "message": err.Error()})
		fmt.Fprintln(stdout, string(b))
		return 3
	}
	fmt.Fprintln(stdout, wire.ErrLine(err))
	return store.MapExit(err)
}
