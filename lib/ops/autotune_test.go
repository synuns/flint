// autotune_test.go — the B3 code-registry regressions (v1.23.0 spec B3):
// TestAutoRulesRegistry (the initial 1-row registry, field completeness, and
// the auto_ signal prefix distinguishing rule signals from diagnosis
// signals), TestAutoTriggerBoundaries (the verdict-query boundaries — sample
// floor, firing, non-firing, and the thresholds-injection proof), and
// TestAccumulationGateCombinations (all 8 combinations of the observation
// accumulation gate rounds >= 14 · fires >= 5 · streak >= 3).
package ops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// autoTriggerObsDefault — a firing observation under the default thresholds
// (sample 20 >= zero_near_min_read 10; 6 of 10 zero rows reclassifiable =
// 60% >= zero_near_warn_pct 30).
func autoTriggerObsDefault() AutoTriggerObs {
	return AutoTriggerObs{ReadEventsN: 20, ZeroN: 10, ReclassN: 6}
}

func TestAutoRulesRegistry(t *testing.T) {
	rules := AutoRules()
	if len(rules) != 1 {
		t.Fatalf("registry rows = %d, want 1 (initial whitelist — spec B3)", len(rules))
	}
	r := rules[0]
	if r.Signal != "auto_zero_near_high" {
		t.Errorf("Signal = %q, want auto_zero_near_high", r.Signal)
	}
	// the auto_ prefix is the load-bearing distinction from the diagnosis
	// signal namespace (zero_near_high lives in diagnosis.go).
	if !strings.HasPrefix(r.Signal, "auto_") {
		t.Errorf("Signal %q lacks the auto_ prefix (diagnosis-signal collision risk)", r.Signal)
	}
	if r.Key != "suggest_min_score" {
		t.Errorf("Key = %q, want suggest_min_score", r.Key)
	}
	if r.Direction != "down" {
		t.Errorf("Direction = %q, want down", r.Direction)
	}
	if r.SafeRange[0] != 0.05 || r.SafeRange[1] != 0.30 {
		t.Errorf("SafeRange = %v, want [0.05 0.30]", r.SafeRange)
	}
	if r.WeeklyMax <= 0 {
		t.Errorf("WeeklyMax = %d, want > 0 (enforced starting v1.24.0)", r.WeeklyMax)
	}
	if r.Auto != "shadow" {
		t.Errorf("Auto = %q, want shadow (v1.23.0 initial grade)", r.Auto)
	}
	if r.SuccessPair == "" {
		t.Error("SuccessPair is empty (the success-pair definition is mandatory)")
	}
	if r.ObsWindowD != 14 {
		t.Errorf("ObsWindowD = %d, want 14 (D=14d, TUNING §0 inheritance)", r.ObsWindowD)
	}
	// the safe range must bracket the registry default (0.15) — a range that
	// excludes the default could not host a downward move.
	if lo, hi := r.SafeRange[0], r.SafeRange[1]; !(lo < hi) {
		t.Errorf("SafeRange [%g,%g] is not an ordered pair", lo, hi)
	}
	// the key must be a real tuning key (the registry must not drift from
	// the tuning surface).
	found := false
	for _, k := range TuningKeyAll() {
		if k == r.Key {
			found = true
		}
	}
	if !found {
		t.Errorf("Key %q is not a tuning key (TuningKeyAll drift)", r.Key)
	}
	// the verdict query template interpolates the window and binds the
	// safe-range floor as $1.
	q := AutoVerdictQuery(r)
	if !strings.Contains(q, fmt.Sprintf("interval '%d days'", r.ObsWindowD)) {
		t.Errorf("query lacks the %dd window: %s", r.ObsWindowD, q)
	}
	if !strings.Contains(q, "near_score >= $1") {
		t.Errorf("query lacks the near_score >= $1 floor filter: %s", q)
	}
	if !strings.Contains(q, "read_events") {
		t.Errorf("query does not read read_events: %s", q)
	}
}

func TestAutoTriggerBoundaries(t *testing.T) {
	rule := AutoRules()[0]
	def := store.DefaultThresholds()

	// boundary 1: sample floor — below zero_near_min_read never fires even
	// with a fully reclassifiable zero set.
	under := autoTriggerObsDefault()
	under.ReadEventsN = def.ZeroNearMinRead - 1
	if v := AutoTrigger(rule, under, def); v.Fired {
		t.Errorf("fired below the sample floor (ReadEventsN=%d < zero_near_min_read=%d): %+v",
			under.ReadEventsN, def.ZeroNearMinRead, v)
	} else if v.SampleOk {
		t.Errorf("SampleOk = true below the sample floor: %+v", v)
	}

	// boundary 2: firing — at the sample floor with the ratio at the warn
	// ratio (>=, inclusive like the diagnosis B1 convention).
	at := autoTriggerObsDefault()
	at.ReadEventsN = def.ZeroNearMinRead
	// ratio exactly zero_near_warn_pct: 3 of 10 zero rows = 30%.
	at.ReclassN = 3
	if v := AutoTrigger(rule, at, def); !v.Fired {
		t.Errorf("did not fire at the boundary (ratio %d%% >= warn %d%%): %+v",
			v.Ratio, def.ZeroNearWarnPct, v)
	}

	// boundary 3: non-firing — sample ok but the ratio one point below.
	below := at
	below.ReclassN = 2 // 20% < 30%
	if v := AutoTrigger(rule, below, def); v.Fired {
		t.Errorf("fired below the warn ratio (20%% < %d%%): %+v", def.ZeroNearWarnPct, v)
	}

	// boundary 4: no zero rows — ratio 0, not fired, no division by zero.
	nozero := autoTriggerObsDefault()
	nozero.ZeroN = 0
	nozero.ReclassN = 0
	if v := AutoTrigger(rule, nozero, def); v.Fired || v.Ratio != 0 {
		t.Errorf("no-zero-window verdict = %+v, want fired=false ratio=0", v)
	}
}

func TestAutoTriggerThresholdsInjection(t *testing.T) {
	rule := AutoRules()[0]
	obs := autoTriggerObsDefault() // ratio 60%

	// the verdict must read zero_near_min_read and zero_near_warn_pct from
	// the injected thresholds — never hardcoded defaults (spec B3: the
	// verdict keys' single source is thresholds).
	strict := store.Thresholds{ZeroNearMinRead: 100, ZeroNearWarnPct: 90}
	if v := AutoTrigger(rule, obs, strict); v.Fired {
		t.Errorf("fired under strict injected thresholds (min_read=100, warn=90%%): %+v", v)
	}
	loose := store.Thresholds{ZeroNearMinRead: 5, ZeroNearWarnPct: 50}
	if v := AutoTrigger(rule, obs, loose); !v.Fired {
		t.Errorf("did not fire under loose injected thresholds (min_read=5, warn=50%%): %+v", v)
	}
	// unset (0) thresholds fall back to the defaults (the unset-0 consumer
	// convention shared with diagThresholds).
	if v := AutoTrigger(rule, obs, store.Thresholds{}); !v.Fired {
		t.Errorf("did not fire with unset (0) thresholds (default fallback): %+v", v)
	}
}

func TestAccumulationGateCombinations(t *testing.T) {
	// all 8 combinations of the 3 conditions (rounds >= 14, fires >= 5,
	// streak >= 3) — eligible only when all hold.
	cases := []struct {
		name             string
		rounds, fires    int
		streak           int
		wantEligible     bool
		wantDetailSubstr string
	}{
		{"none met", 0, 0, 0, false, "rounds"},
		{"streak only", 3, 2, 3, false, "rounds"},
		{"fires only", 3, 5, 1, false, "rounds"},
		{"rounds only", 14, 1, 1, false, "fires"},
		{"rounds+fires", 14, 5, 2, false, "streak"},
		{"rounds+streak", 14, 4, 3, false, "fires"},
		{"fires+streak", 13, 5, 3, false, "rounds"},
		{"all met", 14, 5, 3, true, ""},
	}
	for _, tc := range cases {
		got := AccumulationGate(AccumulationProgress{Rounds: tc.rounds, Fires: tc.fires, Streak: tc.streak})
		if got.Eligible != tc.wantEligible {
			t.Errorf("%s: eligible = %v, want %v (detail %q)", tc.name, got.Eligible, tc.wantEligible, got.Detail)
		}
		if tc.wantDetailSubstr != "" && !strings.Contains(got.Detail, tc.wantDetailSubstr) {
			t.Errorf("%s: detail %q lacks %q", tc.name, got.Detail, tc.wantDetailSubstr)
		}
		if got.Detail == "" {
			t.Errorf("%s: detail is empty (the unmet condition must be named)", tc.name)
		}
	}
	// the boundary values are inclusive.
	if got := AccumulationGate(AccumulationProgress{Rounds: 14, Fires: 5, Streak: 3}); !got.Eligible {
		t.Errorf("inclusive boundary 14/5/3 not eligible: %+v", got)
	}
}
