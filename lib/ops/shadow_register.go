package ops

import (
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

func init() {
	db.RegisterShadowCircuit(shadowRules, shadowVerdict)
}

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

func shadowVerdict(rule db.ShadowRule, obs db.ShadowObs, th store.Thresholds) db.ShadowVerdict {
	v := AutoTrigger(AutoRule{Signal: rule.Signal, Key: rule.Key}, AutoTriggerObs{
		ReadEventsN: obs.ReadEventsN, ZeroN: obs.ZeroN, ReclassN: obs.ReclassN,
	}, th)
	return db.ShadowVerdict{Fired: v.Fired, SampleOk: v.SampleOk, Ratio: v.Ratio, Detail: v.Detail}
}
