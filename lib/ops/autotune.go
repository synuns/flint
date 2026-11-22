// autotune.go — B3 the autotune code registry (v1.23.0 spec B3, schema v18
// shadow circuit). The policy single source for autonomous tuning: which
// signal may tune which key, in which direction, inside which safe range, at
// which auto grade. v1.23.0 is shadow-only — predictions are recorded, no
// value is ever applied (the apply path is the v1.24.0 gate).
//
// The registry is deliberately NOT the diagnosis engine (spec ruling:
// Diagnose reuse is forbidden — its input is the whole ReportOutput). Each
// rule carries its own verdict-query expression, and the verdict thresholds
// (sample floor, warn ratio) read from store.Thresholds — the single source
// (no hardcoded copies). The auto_ signal prefix keeps the rule namespace
// distinct from the diagnosis signal namespace (zero_near_high vs
// auto_zero_near_high).
package ops

import (
	"fmt"
	"strings"

	"github.com/synuns/flint/lib/store"
)

// AutoRule — one row of the autotune code registry. All fields are
// structural policy constants (like the thresholds-registry defaults), not
// personal tuning values — personal values live only in meta overrides.
type AutoRule struct {
	// Signal — the rule's signal id. Must carry the auto_ prefix (the
	// diagnosis-signal namespace separator — the registry guard asserts it).
	Signal string
	// Query — the verdict query expression (a template): $1 binds the rule's
	// safe-range floor, %d interpolates ObsWindowD. Returns one row:
	// sample_n (window read rows), zero_n (zero rows), reclass_n (zero rows
	// with near_score >= the floor — the pool a downward move within the
	// safe range could reclassify as near).
	Query string
	// Key — the tuning key the rule adjusts (must be a TuningKeyAll member).
	Key string
	// Direction — the adjustment direction ("down" | "up").
	Direction string
	// SafeRange — the closed [lo, hi] bounds any proposal must stay within
	// (registry constants — proposals outside are rejected before insert).
	SafeRange [2]float64
	// WeeklyMax — the max applies per calendar week. Enforcement is the
	// v1.24.0 apply phase; v1.23.0 shadow rounds never apply, so the cap is
	// carried but not yet executed (documented, not dead — the apply path
	// reads it in v1.24.0).
	WeeklyMax int
	// Auto — the auto grade ("shadow" = predict-and-record only).
	Auto string
	// SuccessPair — the documented success definition this rule is judged
	// against: zero_near_pct decreases while hit_pct and the read count stay
	// unchanged. A hit means round-over-round stability of the
	// reclassification direction at the minimum sample — NOT causality
	// (the mandatory "a hit is stability, not causality" footnote).
	SuccessPair string
	// ObsWindowD — the observation window days (D=14d, the TUNING §0
	// inheritance — matches the events retention default).
	ObsWindowD int
}

// the initial-row registry constants — suggest_min_score bounds (the safe
// range brackets the registry default 0.15, hosting a downward move).
const (
	autoSuggestMinScoreLo = 0.05
	autoSuggestMinScoreHi = 0.30
	// autoSuggestWeeklyMax — the apply-phase weekly cap (v1.24.0 enforcement).
	autoSuggestWeeklyMax = 1
	// autoObsWindowD — D=14d (spec: the TUNING §0 inheritance).
	autoObsWindowD = 14
)

// autoRules — the registry original (the "register in 1 place + guard test"
// pattern). Initial whitelist = 1 row: suggest_min_score is the only key
// with an exact-causality classification source (read_events.near_score) —
// echo/conflict scores are not stored and diagnostic keys are a masking
// vector, so they stay manual (the G2 ruling).
var autoRules = []AutoRule{
	{
		Signal: "auto_zero_near_high",
		// The portable aggregate form — Doltgres rejects COUNT(*) FILTER
		// ("function filters are not yet supported", SQLSTATE XX000,
		// measured on the first real-DB execution in T4 — the COUNT(CASE)
		// shape is semantically identical). The reclass arm carries the
		// near_score domain guard (BETWEEN 0 AND 1, r101-2): a manually
		// injected out-of-domain score never enters the verdict or the
		// reclassification pool.
		Query: `SELECT COUNT(*) AS sample_n,
       COUNT(CASE WHEN zero THEN 1 END) AS zero_n,
       COUNT(CASE WHEN zero AND near_score BETWEEN 0 AND 1 AND near_score >= $1 THEN 1 END) AS reclass_n
  FROM read_events
 WHERE ts >= now() - interval '%d days'`,
		Key:       "suggest_min_score",
		Direction: "down",
		SafeRange: [2]float64{autoSuggestMinScoreLo, autoSuggestMinScoreHi},
		WeeklyMax: autoSuggestWeeklyMax,
		Auto:      "shadow",
		SuccessPair: "zero_near_pct decreases with hit_pct and the read count unchanged " +
			"(a hit is round-over-round stability, not causality)",
		ObsWindowD: autoObsWindowD,
	},
}

// AutoRules — the registry's deterministic copy (callers must not mutate the
// original).
func AutoRules() []AutoRule {
	return append([]AutoRule(nil), autoRules...)
}

// AutoVerdictQuery — the rule's verdict query with ObsWindowD interpolated
// ($1 stays bound for the safe-range floor at execution time).
func AutoVerdictQuery(r AutoRule) string {
	return fmt.Sprintf(r.Query, r.ObsWindowD)
}

// AutoTriggerObs — the one-row observation AutoVerdictQuery returns: the
// window's read sample and the zero-row near_score distribution summary the
// trigger judges.
type AutoTriggerObs struct {
	ReadEventsN int // window read rows (the sample — the sample-floor input)
	ZeroN       int // zero rows in the window
	ReclassN    int // zero rows with near_score >= the rule's safe-range floor
}

// AutoTriggerVerdict — the trigger evaluation result. Detail always states
// the basis (fired or not — the silent-failure defense: the shadow circuit
// logs nothing, so the verdict text is the audit trail).
type AutoTriggerVerdict struct {
	Fired    bool   // true = this round fires the rule (bump fired_, insert a prediction)
	SampleOk bool   // true = ReadEventsN >= zero_near_min_read
	Ratio    int    // ReclassN share of ZeroN (%), 0 when ZeroN = 0
	Detail   string // the basis sentence (thresholds and values made explicit)
}

// AutoTrigger — the pure trigger verdict for one rule. Mirrors the diagnosis
// B1 criteria shape but on the zero-row near_score distribution: the sample
// floor is zero_near_min_read and the warn ratio is zero_near_warn_pct —
// both read from the injected thresholds (the single source; unset-0 falls
// back to defaults via the diagThresholds convention). A window with no
// zero rows has ratio 0 and never fires (no division by zero).
func AutoTrigger(rule AutoRule, obs AutoTriggerObs, th store.Thresholds) AutoTriggerVerdict {
	th = diagThresholds(th)
	v := AutoTriggerVerdict{SampleOk: obs.ReadEventsN >= th.ZeroNearMinRead}
	if obs.ZeroN > 0 {
		v.Ratio = obs.ReclassN * 100 / obs.ZeroN
	}
	if !v.SampleOk {
		v.Detail = fmt.Sprintf("sample %d < zero_near_min_read %d — no verdict without observation",
			obs.ReadEventsN, th.ZeroNearMinRead)
		return v
	}
	v.Fired = v.Ratio >= th.ZeroNearWarnPct
	verb := "below"
	if v.Fired {
		verb = "at or above"
	}
	v.Detail = fmt.Sprintf("sample %d >= zero_near_min_read %d; reclass %d of %d zero rows = %d%% %s zero_near_warn_pct %d%%",
		obs.ReadEventsN, th.ZeroNearMinRead, obs.ReclassN, obs.ZeroN, v.Ratio, verb, th.ZeroNearWarnPct)
	return v
}

// AccumulationProgress — the observation-accumulation counters (the
// v1.24.0 apply gate inputs): shadow rounds observed (meta
// auto_rounds_<key> — every stamped round, fired or not; r100-1), the
// trigger fires (meta fired_auto_<signal> — trigger-verdict fires,
// whether or not a proposal exists), and the current consecutive-hit
// streak (meta auto_streak_<key>). Rounds and fires are independent
// conditions: a non-fired round counts as a round, and a boundary fire
// (no strict move to propose) counts as a fire.
type AccumulationProgress struct {
	Rounds int
	Fires  int
	Streak int
}

// AccumulationState — the accumulation gate verdict: eligible only when all
// three conditions hold, with Detail naming every unmet condition.
type AccumulationState struct {
	Eligible bool
	Detail   string
}

// the accumulation gate constants (spec: K=3 consecutive, N=5 fires, window
// rounds >= 14 — the D=14d observation window at one round per day).
const (
	accumulationMinRounds = 14
	accumulationMinFires  = 5
	accumulationMinStreak = 3
)

// AccumulationGate — the pure observation-accumulation condition (the
// v1.24.0 apply gate): shadow rounds >= 14 AND trigger fires >= 5 AND
// consecutive-hit streak >= 3. Not met defers the apply phase (the exchange
// order with v1.25.0 per the release-scope principle 3).
func AccumulationGate(p AccumulationProgress) AccumulationState {
	var unmet []string
	if p.Rounds < accumulationMinRounds {
		unmet = append(unmet, fmt.Sprintf("rounds %d/%d", p.Rounds, accumulationMinRounds))
	}
	if p.Fires < accumulationMinFires {
		unmet = append(unmet, fmt.Sprintf("fires %d/%d", p.Fires, accumulationMinFires))
	}
	if p.Streak < accumulationMinStreak {
		unmet = append(unmet, fmt.Sprintf("streak %d/%d", p.Streak, accumulationMinStreak))
	}
	if len(unmet) == 0 {
		return AccumulationState{
			Eligible: true,
			Detail: fmt.Sprintf("accumulation met: rounds >= %d, fires >= %d, streak >= %d",
				accumulationMinRounds, accumulationMinFires, accumulationMinStreak),
		}
	}
	return AccumulationState{
		Eligible: false,
		Detail:   fmt.Sprintf("accumulation not met: %s", strings.Join(unmet, ", ")),
	}
}
