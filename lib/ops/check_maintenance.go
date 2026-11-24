package ops

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"sort"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func checkEventVolume(ctx context.Context, st wire.Store) DoctorCheck {
	return checkEventVolumeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkEventVolumeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "event-volume"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query metrics tables"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT (SELECT count(*) FROM read_events)
		     + (SELECT count(*) FROM session_starts)
		     + (SELECT count(*) FROM hot_events)`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		c.Status = "warn"
		c.Detail = "metrics table aggregation failed — schema v7 may not be migrated"
		return c
	}
	n, _ := render.CellToInt(rows[0][0])
	if n >= th.EventsVolumeWarn {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("metrics tables at %d rows — threshold %d (events_volume_warn). Check retention (events_retention_days, hot_events_retention_days)", n, th.EventsVolumeWarn)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("metrics tables at %d rows", n)
	return c
}

const doltLogDepthName = "dolt_" + "log_depth"

func checkDoltLogDepthTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: doltLogDepthName}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query " + db.DoltLogTable
		return c
	}
	depth, err := db.DoltLogDepth(ctx, st)
	if err != nil {
		c.Status = "warn"
		c.Detail = db.DoltLogTable + " depth query failed: " + err.Error()
		return c
	}
	if depth >= int64(th.DoltLogDepthWarn) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%s depth %d at or above threshold %d (depth_warn) — consider maintain --gc --yes (disconnects)", db.DoltLogTable, depth, th.DoltLogDepthWarn)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%s depth %d", db.DoltLogTable, depth)
	return c
}

func checkDBSizeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "db_size"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query the database size"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT pg_database_size(current_database())`, nil)
	if err != nil {

		c.Status = "skip"
		c.Detail = "pg_database_size unavailable — check skipped (Doltgres does not implement the function — a standing skip there)"
		return c
	}
	bytes := int64(0)
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &bytes)
	}
	mb := int(bytes >> 20)
	if mb >= th.DbSizeWarnMB {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("database size %dMB at or above threshold %dMB (db_size_warn_mb)", mb, th.DbSizeWarnMB)
		c.Fix = "fix: review backup retention — RUNBOOK"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("database size %dMB", mb)
	return c
}

func checkDroppedDBsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "dropped_dbs"}
	if st == nil {
		c.Status = "skip"
		c.Detail = "no store — check skipped"
		return c
	}
	names, ok := listDroppedDatabases(ctx, st)
	if !ok {
		c.Status = "skip"
		c.Detail = "dropped-database catalog unavailable — check skipped (a standing skip)"
		return c
	}
	if len(names) >= th.DroppedDBsWarn {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d dropped database(s) restorable — engine-wide space held (dropped_dbs_warn %d): %s",
			len(names), th.DroppedDBsWarn, strings.Join(names, ", "))
		c.Fix = "fix: flint maintain --purge-dropped --yes (irreversible — the dry-run list shows what is restorable)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d dropped database(s) restorable", len(names))
	return c
}

const strayDBsAllowKey = "stray_dbs_allow"

func strayDBsAllowList(ctx context.Context, st wire.Store) map[string]bool {
	out := map[string]bool{}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT value FROM meta WHERE key = $1`, []any{strayDBsAllowKey})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return out
	}
	for _, p := range strings.Split(fmt.Sprint(rows[0][0]), ",") {
		if n := strings.TrimSpace(p); n != "" {
			out[n] = true
		}
	}
	return out
}

func checkStrayDBsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "stray_dbs"}
	if st == nil {
		c.Status = "skip"
		c.Detail = "no store — check skipped"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT datname FROM pg_database`, nil)
	if err != nil {
		c.Status = "skip"
		c.Detail = "pg_database unavailable — check skipped"
		return c
	}
	allow := strayDBsAllowList(ctx, st)
	for _, b := range []string{"postgres", "template0", "template1"} {
		allow[b] = true
	}
	if u, uerr := url.Parse(PurgeDSNFn()); uerr == nil {
		if opsDB := strings.TrimPrefix(u.Path, "/"); opsDB != "" {
			allow[opsDB] = true
		}
	}
	var stray []string
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		name := strings.TrimSpace(fmt.Sprint(r[0]))
		if name == "" || allow[name] {
			continue
		}
		stray = append(stray, name)
	}
	sort.Strings(stray)
	if len(stray) > th.StrayDBsWarn {
		detail := fmt.Sprintf("%d stray database(s) beyond the allow list (stray_dbs_warn %d): %s",
			len(stray), th.StrayDBsWarn, strings.Join(stray, ", "))
		for _, name := range stray {
			if name == "flint_test" {
				detail += " — flint_test is a legacy shared test DB: DROP then purge"
				break
			}
		}
		c.Status = "warn"
		c.Detail = detail
		c.Fix = "fix: DROP the unwanted databases, then flint maintain --purge-dropped --yes; keep-listed names go to meta stray_dbs_allow (comma list)"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%d stray database(s) beyond the allow list", len(stray))
	return c
}
