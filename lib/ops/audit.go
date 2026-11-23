package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const (
	auditDefaultLimit = 20
	auditMaxLimit     = 100
)

var auditUsage = i18n.T("usage.audit")

type auditEntry struct {
	CommitHash string                    `json:"commit_hash"`
	Committer  string                    `json:"committer"`
	Date       string                    `json:"date"`
	Message    string                    `json:"message"`
	Stat       map[string]auditTableStat `json:"stat,omitempty"`
	DateRel    string                    `json:"date_rel,omitempty"`
}

type auditTableStat struct {
	DataChange   bool `json:"data_change"`
	SchemaChange bool `json:"schema_change"`
}

func RelTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func RunAudit(st wire.Store, args []string, stdout io.Writer) int {
	ctx := context.Background()

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--limit", Value: true},
			{Name: "--stat"},
			{Name: "--session", Value: true},
			{Name: "--telemetry"},
		},
		Usage: auditUsage,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	limit := auditDefaultLimit
	session := res.Value("--session")
	stat := res.Given("--stat")
	telemetry := res.Given("--telemetry")
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--limit":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", "--limit requires a number — "+auditUsage)))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("invalid limit %q — "+auditUsage, o.Value)))
				return 3
			}
			if n < 1 {
				n = auditDefaultLimit
			}
			limit = n
		case "--session":
			if !o.HasValue || o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", "--session requires an id — "+auditUsage)))
				return 3
			}
		}
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}

	q := wire.LogQuery{LatestN: limit}
	if !telemetry {
		q.MessageNotLike = `telemetry: %`
	}
	if session != "" {

		q.MessageLike = `%[session:` + query.EscapeLike(session) + `]%`
	}
	entries, err := st.LogEntries(ctx, q)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	log := make([]auditEntry, 0, len(entries))
	for _, en := range entries {
		e := auditEntry{}

		e.CommitHash = render.Text(en.Hash)
		e.Committer = render.Text(en.Committer)
		if !en.Date.IsZero() {
			e.Date = fmt.Sprint(en.Date)
		}
		e.Message = render.Text(en.Message)
		if stat && !en.Date.IsZero() {
			e.DateRel = RelTime(en.Date)
		}
		log = append(log, e)
	}
	if stat && len(log) > 0 {

		hashes := make([]string, 0, len(log))
		for i := range log {
			if log[i].CommitHash != "" {
				hashes = append(hashes, log[i].CommitHash)
			}
		}
		diffStats, err := st.DiffStat(ctx, hashes)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		stats := make(map[string]map[string]auditTableStat, len(log))
		for _, ds := range diffStats {

			h := render.Text(ds.CommitHash)
			ts := auditTableStat{DataChange: ds.DataChange, SchemaChange: ds.SchemaChange}
			if stats[h] == nil {
				stats[h] = map[string]auditTableStat{}
			}
			stats[h][render.Text(ds.Table)] = ts
		}
		for i := range log {
			if s, ok := stats[log[i].CommitHash]; ok {
				log[i].Stat = s
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"log": log})
	fmt.Fprintln(stdout, string(b))
	return 0
}

func runAudit(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return RunAudit(st, args, stdout)
}
