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

type MergeAuditRow struct {
	ID      int
	Topic   string
	Summary string
}

type MergeAuditPair struct {
	A        int     `json:"a"`
	B        int     `json:"b"`
	TopicA   string  `json:"topic_a"`
	TopicB   string  `json:"topic_b"`
	Score    float64 `json:"score"`
	Relation string  `json:"relation"`
}

type MergeAuditCost struct {
	PairsTotal      int    `json:"pairs_total"`
	MicrosPerPair   int    `json:"micros_per_pair"`
	EstimatedMicros int    `json:"estimated_micros"`
	BudgetMicros    int    `json:"budget_micros"`
	OverBudget      bool   `json:"over_budget"`
	AdvisedCap      int    `json:"advised_cap"`
	EffectiveRows   int    `json:"effective_rows"`
	Advice          string `json:"advice"`
}

var MergeAuditBudgetMicros = 5_000_000

const mergeAuditCalibPairs = 200

func FetchMergeAuditRows(ctx context.Context, st wire.Store, scanCap int) ([]MergeAuditRow, bool, error) {
	if scanCap <= 0 {
		scanCap = 1
	}
	rows, err := wire.QueryOnce(ctx, st,
		"SELECT id, topic, summary FROM knowledge WHERE invalid_at IS NULL AND status = 'active' LIMIT "+
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

func ClassifyMergeAuditPairs(rows []MergeAuditRow, dupThreshold float64) ([]MergeAuditPair, MergeAuditCost) {
	m := len(rows)
	cost := MergeAuditCost{
		PairsTotal:    m * (m - 1) / 2,
		BudgetMicros:  MergeAuditBudgetMicros,
		EffectiveRows: m,
	}
	combined := make([]string, m)
	for i := range rows {

		combined[i] = render.TruncateRunes(rows[i].Topic+" "+rows[i].Summary, echoInputMaxRunes)
	}

	calib := cost.PairsTotal
	if calib > mergeAuditCalibPairs {
		calib = mergeAuditCalibPairs
	}
	if calib > 0 {
		var calibSink float64
		start := time.Now()
		n := 0
		for i := 0; i < m && n < calib; i++ {
			for j := i + 1; j < m && n < calib; j++ {
				calibSink = query.BigramDiceMultiset(combined[i], combined[j])
				n++
			}
		}
		_ = calibSink
		perPairNs := float64(time.Since(start).Nanoseconds()) / float64(n)
		cost.MicrosPerPair = int(math.Ceil(perPairNs / 1000.0))
	}
	cost.EstimatedMicros = cost.MicrosPerPair * cost.PairsTotal

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

	pairs := []MergeAuditPair{}
	for i := 0; i < cost.EffectiveRows; i++ {
		for j := i + 1; j < cost.EffectiveRows; j++ {
			score := query.BigramDiceMultiset(combined[i], combined[j])
			if score < dupThreshold {
				continue
			}

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
