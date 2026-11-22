// shadow_register.go — the autotune shadow-circuit composition (v1.23.0
// T4, team-lead ruling): lib/db can never import lib/ops (ops imports
// db — the only legal direction, compile-verified in T4), so the
// registry projection is injected from THIS side at init — the same
// shape as cmd/flint's blank import of lib/mcp for the reverse-reference
// injection. Any binary importing ops (lib/cli does) carries the wiring;
// until then db's shadow hook stays a nil check (the unconfigured
// zero-cost default).
package ops

import (
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

// init — registers the real circuit into the db seam (rules + verdict —
// the single sources; db never re-implements either).
func init() {
	db.RegisterShadowCircuit(shadowRules, shadowVerdict)
}

// shadowRules — the AutoRules registry projected onto db.ShadowRule
// (AutoVerdictQuery performs the ObsWindowD interpolation; the shadow
// round binds $1 to the safe-range floor at execution time).
func shadowRules() []db.ShadowRule {
	out := make([]db.ShadowRule, 0, len(AutoRules()))
	for _, r := range AutoRules() {
		out = append(out, db.ShadowRule{
			Signal:      r.Signal,
			Query:       AutoVerdictQuery(r),
			Key:         r.Key,
			Direction:   r.Direction,
			SafeRange:   r.SafeRange,
			Auto:        r.Auto,
			SuccessPair: r.SuccessPair,
			ObsWindowD:  r.ObsWindowD,
		})
	}
	return out
}

// shadowVerdict — the AutoTrigger adapter: the injected pure verdict on
// the db-side projections (thresholds stay the store single source).
func shadowVerdict(rule db.ShadowRule, obs db.ShadowObs, th store.Thresholds) db.ShadowVerdict {
	v := AutoTrigger(AutoRule{Signal: rule.Signal, Key: rule.Key}, AutoTriggerObs{
		ReadEventsN: obs.ReadEventsN, ZeroN: obs.ZeroN, ReclassN: obs.ReclassN,
	}, th)
	return db.ShadowVerdict{Fired: v.Fired, SampleOk: v.SampleOk, Ratio: v.Ratio, Detail: v.Detail}
}
