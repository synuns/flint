package ops

import (
	"context"
	"fmt"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const collectionFreshnessKey = "collection_freshness_hours"

const collectionFreshnessFix = "fix: run a flint session (the session-start hook absorbs it) — or inspect hook wiring via flint doctor --check hook-path"

func CheckFreshness(ctx context.Context, st wire.Store) DoctorCheck {
	return CheckFreshnessTh(ctx, st, knowledge.WriteThresholds(ctx, st))
}

func CheckFreshnessTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "collection-freshness"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query collection freshness"
		return c
	}
	limitH := store.ClampThresholdInt(collectionFreshnessKey, th.CollectionFreshnessH)
	rows, err := wire.QueryOnce(ctx, st, `SELECT max(ts) FROM session_starts`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "session_starts query failed"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("no session_starts rows — collection not observed within %s %dh", collectionFreshnessKey, limitH)
		c.Fix = collectionFreshnessFix
		return c
	}
	ts, ok := wire.CellToTime(rows[0][0])
	if !ok {
		c.Status = "warn"
		c.Detail = "session_starts max(ts) unparsable"
		return c
	}
	if ts.After(time.Now()) {
		c.Status = "warn"
		c.Detail = "session_starts max(ts) is in the future — clock skew or corrupt row"
		return c
	}
	ageH := int(time.Since(ts).Hours())
	if ageH > limitH {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("last session_start %dh old — exceeds %s %dh", ageH, collectionFreshnessKey, limitH)
		c.Fix = collectionFreshnessFix
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("last session_start %dh old (within %s %dh)", ageH, collectionFreshnessKey, limitH)
	return c
}
