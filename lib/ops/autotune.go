package ops

import (
	"fmt"
	"strings"

	"github.com/synuns/flint/lib/store"
)

type AutoRule struct {
	Signal string

	Query string

	Key string

	Direction string

	SafeRange [2]float64

	WeeklyMax int

	Auto string

	SuccessPair string

	ObsWindowD int
}

const (
	autoSuggestMinScoreLo = 0.05
	autoSuggestMinScoreHi = 0.30

	autoSuggestWeeklyMax = 1

	autoObsWindowD = 14
)

var autoRules = []AutoRule{
	{
		Signal: "auto_zero_near_high",

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

func AutoRules() []AutoRule {
	return append([]AutoRule(nil), autoRules...)
}

func AutoVerdictQuery(r AutoRule) string {
	return fmt.Sprintf(r.Query, r.ObsWindowD)
}

type AutoTriggerObs struct {
	ReadEventsN int
	ZeroN       int
	ReclassN    int
}

type AutoTriggerVerdict struct {
	Fired    bool
	SampleOk bool
	Ratio    int
	Detail   string
}

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

type AccumulationProgress struct {
	Rounds int
	Fires  int
	Streak int
}

type AccumulationState struct {
	Eligible bool
	Detail   string
}

const (
	accumulationMinRounds = 14
	accumulationMinFires  = 5
	accumulationMinStreak = 3
)

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
