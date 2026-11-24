package ops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func checkRecallStarved(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "recall-starved"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query the recall counter"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT value FROM meta WHERE key = $1`, []any{"recall_starved_count"})
	if err != nil {
		c.Status = "warn"
		c.Detail = "recall counter query failed"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		c.Status = "ok"
		c.Detail = "no recall-starved history"
		return c
	}
	if n, ok := render.CellToInt(rows[0][0]); ok && n > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d recall-starved sessions cumulated — the done warning may have been hidden by the minimal profile or hook wiring", n)
		c.Fix = "fix: run flint read before the next session starts — once resolved, remove the counter via psql (meta key recall_starved_count)"
		return c
	}
	c.Status = "ok"
	return c
}

var meteringFreshnessSources = []struct {
	label string
	sql   string
}{
	{"read_events", `SELECT max(ts) FROM read_events`},
	{"recall_events", `SELECT max(recalled_at) FROM recall_events`},
	{"hot_events", `SELECT max(ts) FROM hot_events`},
	{"knowledge_reads", `SELECT max(ts) FROM knowledge_reads`},
	{"session_starts", `SELECT max(ts) FROM session_starts`},
}

var meteringFreshnessGateKeys = []string{"last_threshold_snapshot_at", "last_rollup_at"}

func checkMeteringFreshnessTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "metering-freshness"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query metering freshness"
		return c
	}

	limit := db.HoursSpan(th.MeteringFreshnessWarnH, store.DefaultThresholds().MeteringFreshnessWarnH)
	warnH := int(limit.Hours())
	var stale []string
	judged := 0
	now := time.Now()
	over := func(label string, ts time.Time) {
		judged++

		if ts.After(now) {
			stale = append(stale, fmt.Sprintf("%s future", label))
			return
		}
		if d := now.Sub(ts); d > limit {
			stale = append(stale, fmt.Sprintf("%s %dh", label, int(d.Hours())))
		}
	}
	for _, s := range meteringFreshnessSources {
		rows, err := wire.QueryOnce(ctx, st, s.sql, nil)
		if err != nil {
			continue
		}
		if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
			judged++
			stale = append(stale, s.label+" (never)")
			continue
		}
		if t, ok := CellToTime(rows[0][0]); ok {
			over(s.label, t)
		}
	}

	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_threshold_snapshot_at','last_rollup_at')`, nil); err == nil {
		stamps := map[string]time.Time{}

		corrupt := map[string]bool{}
		for _, r := range rows {
			if len(r) != 2 || r[0] == nil || r[1] == nil {
				continue
			}
			if ts, perr := time.Parse(time.RFC3339, fmt.Sprint(r[1])); perr == nil {
				stamps[fmt.Sprint(r[0])] = ts
			} else {
				corrupt[fmt.Sprint(r[0])] = true
			}
		}
		for _, k := range meteringFreshnessGateKeys {
			if ts, ok := stamps[k]; ok {
				over(k, ts)
				continue
			}
			judged++
			if corrupt[k] {
				stale = append(stale, k+" (corrupt)")
				continue
			}
			stale = append(stale, k+" (never)")
		}
	}
	if judged == 0 {
		c.Status = "ok"
		c.Detail = "metering freshness unavailable — verdict deferred"
		return c
	}
	if len(stale) > 0 {
		c.Status = "warn"

		c.Detail = fmt.Sprintf("%d of %d sources over %dh: %s",
			len(stale), judged, warnH, strings.Join(stale, ", "))
		c.Fix = "fix: run a flint read (the metering path) — the stale sources then refresh"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d sources within %dh", judged, warnH)
	return c
}
