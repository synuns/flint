// diagnosis.go — rule diagnostics D (spec §3). From the effect, counts, and daily
// data collected by report (ReportOutput) plus thresholds, it evaluates the 7 rules (8 items — the hot
// budget band is one rule judging both directions) and returns a structured item array.
// Pure functions (0 LLMs — E-packet deferred); normal silence is valid (an empty array when nothing is wrong).
//
// The 3-tier criteria basis (spec §3): A self-reference (hot mean/budget band, hot bytes trend, hit
// cost vs savings lower bound), B registry-inherited reads (zero_near and pending_age inherit
// doctor's existing keys — preventing duplicate keys; dead uses diag_dead_warn), C value defaults
// (the recall_led 'default goal' label — a habitual-goal notation, not a personal goal).
//
// User principle: diagnostics are a single data model — text (sentence
// render, Sentence), --json (the ReportOutput.Diagnosis array), and --html (cards) — all three
// views render from the same ReportOutput.Diagnosis (no data split across views).
package ops

import (
	"fmt"

	"github.com/synuns/flint/lib/store"
)

// DiagnosisItem — one diagnostic item. The sentence contract (spec §3): signal + value +
// basis source + action, each structured as one field, with Sentence() joining them into 1
// line. tuning_key is the meta override key leading to the action (empty means no tuning key for that rule).
type DiagnosisItem struct {
	Signal    string `json:"signal"`               // signal id (kebab-case)
	Value     string `json:"value"`                // value summary (the observation)
	Basis     string `json:"basis"`                // basis source (tier, key, and default made explicit)
	Action    string `json:"action"`               // the 1 recommended action
	TuningKey string `json:"tuning_key,omitempty"` // the basis-adjusting meta key (omitted when absent)
}

// Sentence — the text-view sentence render (signal + value + basis source + action in 1 line).
func (d DiagnosisItem) Sentence() string {
	return fmt.Sprintf("%s: %s (basis: %s) → %s", d.Signal, d.Value, d.Basis, d.Action)
}

// Diagnose — the pure-function rule engine. Inputs are report's collection output
// (ReportOutput) plus thresholds and hot_budget (meta hot_budget — the knowledge.HotBudget
// loaded value). Normal silence: if no rule triggers, it returns an empty array (non-nil —
// JSON []). Unset (0) thresholds keys fall back to DefaultThresholds (the consumer-side
// completion of the ParseThresholds unset-0 convention).
func Diagnose(out ReportOutput, th store.Thresholds, hotBudget int) []DiagnosisItem {
	th = diagThresholds(th)
	e := out.Effect
	items := make([]DiagnosisItem, 0, 8)

	// ── tier A: self-referencing criteria ──
	// A1/A2 hot injection mean vs the hot_budget band (0 calls or 0 budget means no verdict —
	// the zero-denominator convention). The mean is effect.bytes.hot.avg_bytes (the same source
	// as the daily sums).
	if avg := e.Bytes.Hot.AvgBytes; avg > 0 && hotBudget > 0 {
		ratio := float64(avg) / float64(hotBudget)
		pct := int(ratio * 100)
		if ratio >= th.DiagHotBudgetHigh {
			items = append(items, DiagnosisItem{
				Signal:    "hot_budget_saturation",
				Value:     fmt.Sprintf("hot injection avg %d bytes = %d%% of budget %d bytes", avg, pct, hotBudget),
				Basis:     fmt.Sprintf("diag_hot_budget_high=%g (injection avg ratio vs hot_budget)", th.DiagHotBudgetHigh),
				Action:    "possible chronic truncation — consider raising hot_budget (summations keep hitting the cap)",
				TuningKey: "hot_budget",
			})
		} else if ratio <= th.DiagHotBudgetLow {
			items = append(items, DiagnosisItem{
				Signal:    "hot_budget_slack",
				Value:     fmt.Sprintf("hot injection avg %d bytes = %d%% of budget %d bytes", avg, pct, hotBudget),
				Basis:     fmt.Sprintf("diag_hot_budget_low=%g (injection avg ratio vs hot_budget)", th.DiagHotBudgetLow),
				Action:    "informational — ample budget headroom (lowering hot_budget could save injection bytes)",
				TuningKey: "hot_budget",
			})
		}
	}

	// A3 hot bytes trend — comparing the daily hot_bytes sums of the window's first half vs
	// second half. A zero first-half sum (new usage, etc.) means no comparison. |change| >= diag_trend_pct% is a sharp shift.
	if cur, prev, ok := hotBytesHalves(out.Days); ok && prev > 0 {
		delta := (cur - prev) * 100 / prev
		if pct := absInt(delta); pct >= th.DiagTrendPct {
			sign := "+"
			if delta < 0 {
				sign = ""
			}
			items = append(items, DiagnosisItem{
				Signal:    "hot_bytes_trend",
				Value:     fmt.Sprintf("hot bytes second half %d vs first half %d (%s%d%%)", cur, prev, sign, delta),
				Basis:     fmt.Sprintf("diag_trend_pct=%d%% (first vs second half daily hot_bytes sums)", th.DiagTrendPct),
				Action:    "sudden injection size change — check hot composition and provisioning changes",
				TuningKey: "diag_trend_pct",
			})
		}
	}

	// A4 injection cost vs savings lower bound — reported when cost.hit_injection_cost_bytes
	// exceeds savings_lower_bound_bytes (savings is a 'lower-bound estimate', so exceeding is
	// not proof of net loss — the label wording contract, spec §2.3). A zero savings
	// lower bound means no comparison source, so no verdict.
	if s, c := e.Cost.SavingsLowerBoundBytes, e.Cost.HitInjectionCostBytes; s > 0 && c > s {
		items = append(items, DiagnosisItem{
			Signal: "hit_cost_exceeds_savings",
			Value:  fmt.Sprintf("injection cost %d bytes > savings lower bound %d bytes", c, s),
			Basis:  "cost.hit_injection_cost_bytes vs cost.savings_lower_bound_bytes (savings is a lower-bound estimate — exceeding it does not prove net loss)",
			Action: "review recall payload size and exposure count (room to cut injection cost)",
		})
	}

	// ── tier B: registry (doctor) inherited reads ──
	// B1 zero_near — inheriting doctor's zero-near criteria (sample floor zero_near_min_read
	// and warning ratio zero_near_warn_pct — no new diag keys are created).
	if e.ReadEventsN >= th.ZeroNearMinRead && out.ZeroNearPct >= th.ZeroNearWarnPct {
		items = append(items, DiagnosisItem{
			Signal:    "zero_near_high",
			Value:     fmt.Sprintf("zero_near_pct %d%% (near hits among %d reads)", out.ZeroNearPct, e.ReadEventsN),
			Basis:     fmt.Sprintf("inherits doctor zero-near zero_near_warn_pct=%d%%, sample floor zero_near_min_read=%d", th.ZeroNearWarnPct, th.ZeroNearMinRead),
			Action:    "possible vocabulary mismatch — room to improve search quality (review suggestion scans and tag vocabulary)",
			TuningKey: "zero_near_warn_pct",
		})
	}

	// B2 dead — dead_pct(report effect G3) ≥ diag_dead_warn.
	if e.DeadPct >= th.DiagDeadWarn {
		items = append(items, DiagnosisItem{
			Signal:    "dead_knowledge",
			Value:     fmt.Sprintf("dead_pct %d%% (active but never recalled, long elapsed)", e.DeadPct),
			Basis:     fmt.Sprintf("diag_dead_warn=%d%% (dead_pct at dead_days=%dd)", th.DiagDeadWarn, th.DeadDays),
			Action:    "consider cleanup with maintain --stale and --prune-invalidated",
			TuningKey: "diag_dead_warn",
		})
	}

	// B3 pending backlog — inheriting doctor's pending-age threshold days (the oldest elapsed
	// is not in the report sources, so it reports the count — the threshold days are stated in the sentence).
	if out.Counts.Pending > 0 {
		items = append(items, DiagnosisItem{
			Signal:    "pending_backlog",
			Value:     fmt.Sprintf("%d pending items awaiting approval", out.Counts.Pending),
			Basis:     fmt.Sprintf("inherits doctor pending-age pending_age_days=%dd (age threshold)", th.PendingAgeDays),
			Action:    "review via approve or clean up with maintain --expire-pending",
			TuningKey: "pending_age_days",
		})
	}

	// ── tier C: value defaults ──
	// recall_led < diag_recall_led_goal — the 'default goal' label (a value default —
	// not a personal goal). The sample floor inherits the zero_near convention (no verdict without observation).
	if e.ReadEventsN >= th.ZeroNearMinRead && e.RecallLedPct < th.DiagRecallLedGoal {
		items = append(items, DiagnosisItem{
			Signal:    "recall_led_below_goal",
			Value:     fmt.Sprintf("recall-led sessions %d%%", e.RecallLedPct),
			Basis:     fmt.Sprintf("default goal diag_recall_led_goal=%d%% (value default — a habit goal, not a personal target)", th.DiagRecallLedGoal),
			Action:    "encourage recall-first session habits (review the nudge recall guidance)",
			TuningKey: "diag_recall_led_goal",
		})
	}
	return items
}

// diagThresholds — the default fallback for unset (0, invalid) diagnostic keys (the
// consumer-side completion of the ParseThresholds unset-0 convention). The tier-B inherited
// keys (zero_near, pending_age) are also filled here (a defense for direct callers passing pre-fallback values).
func diagThresholds(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	if th.DiagHotBudgetHigh <= 0 {
		th.DiagHotBudgetHigh = def.DiagHotBudgetHigh
	}
	if th.DiagHotBudgetLow <= 0 {
		th.DiagHotBudgetLow = def.DiagHotBudgetLow
	}
	if th.DiagTrendPct <= 0 {
		th.DiagTrendPct = def.DiagTrendPct
	}
	if th.DiagRecallLedGoal <= 0 {
		th.DiagRecallLedGoal = def.DiagRecallLedGoal
	}
	if th.DiagDeadWarn <= 0 {
		th.DiagDeadWarn = def.DiagDeadWarn
	}
	if th.DeadDays <= 0 {
		th.DeadDays = def.DeadDays
	}
	if th.ZeroNearMinRead <= 0 {
		th.ZeroNearMinRead = def.ZeroNearMinRead
	}
	if th.ZeroNearWarnPct <= 0 {
		th.ZeroNearWarnPct = def.ZeroNearWarnPct
	}
	if th.PendingAgeDays <= 0 {
		th.PendingAgeDays = def.PendingAgeDays
	}
	return th
}

// hotBytesHalves — the first/second-half hot_bytes sums of the days window. A window of 1
// day or less, or an empty first half, means ok=false (no valid trend comparison).
func hotBytesHalves(days []ReportDay) (cur, prev int, ok bool) {
	if len(days) < 2 {
		return 0, 0, false
	}
	half := len(days) / 2
	for _, d := range days[:half] {
		prev += d.HotBytes
	}
	for _, d := range days[half:] {
		cur += d.HotBytes
	}
	return cur, prev, true
}

// absInt — absolute value (for ± trend verdicts).
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
