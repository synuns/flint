// mergeaudit.go — v1.24.0 T3: the merge-audit pair scan. Lists active
// knowledge row PAIRS whose combined-text multiset Dice is at or above
// conflict_dup_threshold (the EchoScan duplicate ruling — the same
// measure, applied row-to-row instead of incoming-to-row). Listing only:
// disposition stays with the existing approval circuit (maintain --merge
// / --supersede) — the decay-audit convention, no self-merging. No new
// table (pure computation over the bounded scan).
package knowledge

import (
	"context"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

// MergeAuditRow — one active-row scan input (the EchoScan row shape).
type MergeAuditRow struct {
	ID      int
	Topic   string
	Summary string
}

// MergeAuditPair — one listed duplicate pair. A < B always. Score is the
// rune-bigram multiset Dice (BigramDiceMultiset) of the combined texts
// (topic+" "+summary per side, the EchoScan comparison basis); Relation
// reuses the echo classification (at or above the threshold is the
// duplicates class by construction).
type MergeAuditPair struct {
	A        int     `json:"a"`
	B        int     `json:"b"`
	TopicA   string  `json:"topic_a"`
	TopicB   string  `json:"topic_b"`
	Score    float64 `json:"score"`
	Relation string  `json:"relation"`
}

// MergeAuditCost — the pre-cost estimate publication (the O(n²) defense):
// the pair count over the scanned rows (min(cap,n)×(min(cap,n)−1)/2),
// the measured per-pair cost (a calibration sample, ceil — a
// conservative publication), and the estimated total. When the estimate
// exceeds the budget the scan is truncated to the largest affordable row
// count (AdvisedCap — also the published "reduce suggest_scan_cap"
// guidance) and EffectiveRows names what was actually compared.
type MergeAuditCost struct {
	PairsTotal      int    `json:"pairs_total"`
	MicrosPerPair   int    `json:"micros_per_pair"`
	EstimatedMicros int    `json:"estimated_micros"`
	BudgetMicros    int    `json:"budget_micros"`
	OverBudget      bool   `json:"over_budget"`
	AdvisedCap      int    `json:"advised_cap"` // 0 = no reduction advice
	EffectiveRows   int    `json:"effective_rows"`
	Advice          string `json:"advice"` // "" when within budget
}

// MergeAuditBudgetMicros — the scan time budget of one merge-audit run
// (a formula constant, not a tuning value — the stale-score weights
// convention). 5 seconds: a curation report is interactive, and past it
// the cap reduction guidance takes over.
var MergeAuditBudgetMicros = 5_000_000

// mergeAuditCalibPairs — the calibration sample size for the measured
// per-pair cost (the first comparisons of the same double loop — no
// extra rows, no extra pass over the data).
const mergeAuditCalibPairs = 200

// FetchMergeAuditRows — the bounded active-row fetch: id, topic, summary
// of the invalid_at IS NULL rows, LIMIT scan_cap+1 (one lookahead row —
// a full page sets truncated, naming the cap rather than silently
// comparing a prefix, the EchoScan query shape plus the truncation
// flag).
func FetchMergeAuditRows(ctx context.Context, st wire.Store, scanCap int) ([]MergeAuditRow, bool, error) {
	if scanCap <= 0 {
		scanCap = 1
	}
	rows, err := wire.QueryOnce(ctx, st,
		"SELECT id, topic, summary FROM knowledge WHERE invalid_at IS NULL LIMIT "+
			strconv.Itoa(scanCap+1), nil)
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > scanCap
	if truncated {
		rows = rows[:scanCap]
	}
	out := make([]MergeAuditRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		summary, _ := r[2].(string)
		out = append(out, MergeAuditRow{ID: id, Topic: topic, Summary: summary})
	}
	return out, truncated, nil
}

// ClassifyMergeAuditPairs — the pairwise scan: every row pair (a<b)
// whose combined-text multiset Dice >= dupThreshold is listed, sorted
// score-descending with ascending (a,b) tie-break. The pre-cost estimate
// rides along: a calibration sample of the same comparisons measures
// µs/pair (ceil — conservative), pairs_total×µs/pair is published BEFORE
// the full pass commits, and an estimate over MergeAuditBudgetMicros
// truncates the scan to the largest affordable row prefix (the advised
// cap — the O(n²) defense; pairs spanning dropped rows are never
// compared). Pure computation — no store access, no writes (listing
// only).
func ClassifyMergeAuditPairs(rows []MergeAuditRow, dupThreshold float64) ([]MergeAuditPair, MergeAuditCost) {
	m := len(rows)
	cost := MergeAuditCost{
		PairsTotal:    m * (m - 1) / 2,
		BudgetMicros:  MergeAuditBudgetMicros,
		EffectiveRows: m,
	}
	combined := make([]string, m)
	for i := range rows {
		// the EchoScan comparison basis: topic+" "+summary, rune-capped.
		combined[i] = render.TruncateRunes(rows[i].Topic+" "+rows[i].Summary, echoInputMaxRunes)
	}
	// phase 1 — calibration: time the first min(total, calib) comparisons
	// of the same double loop (no extra pass; the sample is discarded —
	// phase 2 recomputes everything within the effective prefix).
	calib := cost.PairsTotal
	if calib > mergeAuditCalibPairs {
		calib = mergeAuditCalibPairs
	}
	if calib > 0 {
		start := time.Now()
		n := 0
		for i := 0; i < m && n < calib; i++ {
			for j := i + 1; j < m && n < calib; j++ {
				query.BigramDiceMultiset(combined[i], combined[j])
				n++
			}
		}
		perPairNs := float64(time.Since(start).Nanoseconds()) / float64(n)
		cost.MicrosPerPair = int(math.Ceil(perPairNs / 1000.0))
	}
	cost.EstimatedMicros = cost.MicrosPerPair * cost.PairsTotal
	// phase 2 — budget gate: over budget, shrink to the largest m' with
	// m'(m'−1)/2 pairs inside the budget (a row prefix — stable ids).
	if cost.MicrosPerPair > 0 && cost.EstimatedMicros > cost.BudgetMicros {
		affordable := 1
		for mp := m; mp >= 1; mp-- {
			if mp*(mp-1)/2*cost.MicrosPerPair <= cost.BudgetMicros {
				affordable = mp
				break
			}
		}
		cost.OverBudget = true
		cost.AdvisedCap = affordable
		cost.EffectiveRows = affordable
		cost.Advice = "estimated scan over budget — scan reduced to " + strconv.Itoa(affordable) +
			" rows; reduce suggest_scan_cap to <= " + strconv.Itoa(affordable)
	}
	// phase 3 — the full pairwise pass over the effective prefix.
	pairs := []MergeAuditPair{}
	for i := 0; i < cost.EffectiveRows; i++ {
		for j := i + 1; j < cost.EffectiveRows; j++ {
			score := query.BigramDiceMultiset(combined[i], combined[j])
			if score < dupThreshold {
				continue
			}
			// Re-sanitize stored values on output (the render choke point).
			pairs = append(pairs, MergeAuditPair{
				A:        rows[i].ID,
				B:        rows[j].ID,
				TopicA:   render.Text(rows[i].Topic),
				TopicB:   render.Text(rows[j].Topic),
				Score:    score,
				Relation: EchoRelationDuplicates,
			})
		}
	}
	sort.SliceStable(pairs, func(x, y int) bool {
		if pairs[x].Score != pairs[y].Score {
			return pairs[x].Score > pairs[y].Score
		}
		if pairs[x].A != pairs[y].A {
			return pairs[x].A < pairs[y].A
		}
		return pairs[x].B < pairs[y].B
	})
	return pairs, cost
}
