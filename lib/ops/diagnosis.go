package ops

import (
	"fmt"
	"strings"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

type DiagnosisItem struct {
	Signal    string `json:"signal"`
	Value     string `json:"value"`
	Basis     string `json:"basis"`
	Action    string `json:"action"`
	TuningKey string `json:"tuning_key,omitempty"`
}

func (d DiagnosisItem) Sentence() string {
	return fmt.Sprintf("%s: %s (basis: %s) → %s", d.Signal, d.Value, d.Basis, d.Action)
}

func Diagnose(out ReportOutput, th store.Thresholds, hotBudget int) []DiagnosisItem {
	th = diagThresholds(th)
	e := out.Effect
	items := make([]DiagnosisItem, 0, 8)

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

	if s, c := e.Cost.SavingsLowerBoundBytes, e.Cost.HitInjectionCostBytes; s > 0 && c > s {
		items = append(items, DiagnosisItem{
			Signal: "hit_cost_exceeds_savings",
			Value:  fmt.Sprintf("injection cost %d bytes > savings lower bound %d bytes", c, s),
			Basis:  "cost.hit_injection_cost_bytes vs cost.savings_lower_bound_bytes (savings is a lower-bound estimate — exceeding it does not prove net loss)",
			Action: "review recall payload size and exposure count (room to cut injection cost)",
		})
	}

	if e.ReadEventsN >= th.ZeroNearMinRead && out.ZeroNearPct >= th.ZeroNearWarnPct {
		items = append(items, DiagnosisItem{
			Signal:    "zero_near_high",
			Value:     fmt.Sprintf("zero_near_pct %d%% (near hits among %d reads)", out.ZeroNearPct, e.ReadEventsN),
			Basis:     fmt.Sprintf("inherits doctor zero-near zero_near_warn_pct=%d%%, sample floor zero_near_min_read=%d", th.ZeroNearWarnPct, th.ZeroNearMinRead),
			Action:    "possible vocabulary mismatch — room to improve search quality (review suggestion scans and tag vocabulary)",
			TuningKey: "zero_near_warn_pct",
		})
	}

	if e.DeadPct >= th.DiagDeadWarn {
		items = append(items, DiagnosisItem{
			Signal:    "dead_knowledge",
			Value:     fmt.Sprintf("dead_pct %d%% (active but never recalled, long elapsed)", e.DeadPct),
			Basis:     fmt.Sprintf("diag_dead_warn=%d%% (dead_pct at dead_days=%dd)", th.DiagDeadWarn, th.DeadDays),
			Action:    "consider cleanup with maintain --stale and --prune-invalidated",
			TuningKey: "diag_dead_warn",
		})
	}

	if out.Counts.Pending > 0 {
		items = append(items, DiagnosisItem{
			Signal:    "pending_backlog",
			Value:     fmt.Sprintf("%d pending items awaiting approval", out.Counts.Pending),
			Basis:     fmt.Sprintf("inherits doctor pending-age pending_age_days=%dd (age threshold)", th.PendingAgeDays),
			Action:    "review via approve or clean up with maintain --expire-pending",
			TuningKey: "pending_age_days",
		})
	}

	if e.ReadEventsN >= th.ZeroNearMinRead && e.RecallLedPct < th.DiagRecallLedGoal {
		items = append(items, DiagnosisItem{
			Signal:    "recall_led_below_goal",
			Value:     fmt.Sprintf("recall-led sessions %d%%", e.RecallLedPct),
			Basis:     fmt.Sprintf("default goal diag_recall_led_goal=%d%% (value default — a habit goal, not a personal target)", th.DiagRecallLedGoal),
			Action:    "encourage recall-first session habits (review the nudge recall guidance)",
			TuningKey: "diag_recall_led_goal",
		})
	}

	if silent := silentMeteringKinds(out); len(silent) > 0 {
		disp := silent
		if len(disp) > 5 {
			disp = disp[:5]
		}
		items = append(items, DiagnosisItem{
			Signal: "metering_silent",
			Value: fmt.Sprintf("%d registry kinds never fired over the %dd window: %s",
				len(silent), diagSilentMinDays, strings.Join(disp, ", ")),
			Basis: fmt.Sprintf("observation-kind registry (%d keys, 3 groups) vs report window sums — silent window %dd",
				meteringRegistryTotal(), diagSilentMinDays),
			Action: "check the metering wiring of the silent kinds (or retire the registration) — informational, not breakage",
		})
	}
	return items
}

const diagSilentMinDays = 30

func meteringRegistryTotal() int {
	n := 0
	for _, g := range db.MeteringKindRegistry {
		n += len(g.Kinds)
	}
	return n
}

func silentMeteringKinds(out ReportOutput) []string {
	if len(out.Days) < diagSilentMinDays {
		return nil
	}
	counts := map[string]int{}
	bytes := map[string]int{}
	for _, d := range out.Days {
		counts["write"] += d.Write
		counts["hot"] += d.Hot
		counts["read"] += d.Read
		counts["dup"] += d.Dup
		counts["dup_hook"] += d.DupHook
		counts["dup_manual"] += d.DupManual
		counts["dup_mcp"] += d.DupMcp
		counts["dup_correction"] += d.DupCorrection
		counts["dup_gap"] += d.DupGap
		counts["brief"] += d.Brief
		bytes["read"] += d.ReadBytes
		bytes["hot"] += d.HotBytes
		bytes["brief"] += d.BriefBytes
	}
	curation := map[string]int{
		"approve_done":          out.Curation.ApproveDone,
		"approve_not_found":     out.Curation.ApproveNotFound,
		"approve_supersede_err": out.Curation.ApproveSupersedeErr,
		"maintain_supersede":    out.Curation.MaintainSupersede,
		"maintain_expire":       out.Curation.MaintainExpire,
		"maintain_prune_rows":   out.Curation.MaintainPruneRows,
		"import_added":          out.Curation.ImportAdded,
		"import_skipped":        out.Curation.ImportSkipped,
	}
	var silent []string
	for _, k := range db.MeteringKinds("usage") {
		if counts[k] == 0 {
			silent = append(silent, "usage:"+k)
		}
	}
	for _, k := range db.MeteringKinds("curation") {
		if v, ok := curation[k]; ok && v == 0 {
			silent = append(silent, "curation:"+k)
		}
	}
	for _, k := range db.MeteringKinds("bytes") {
		if bytes[k] == 0 {
			silent = append(silent, "bytes:"+k)
		}
	}
	return silent
}

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

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
