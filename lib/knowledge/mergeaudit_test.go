// mergeaudit_test.go — v1.24.0 T3: the merge-audit pair scan contract
// (pure scoring, threshold boundary, scan cap, pre-cost estimate — the
// fake-store seam; the real-DB command path is the ops slow test).
package knowledge

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

// The merge-audit fixture strings (synthetic demo). The boundary pair
// combines to texts whose rune-bigram multiset Dice is exactly 0.6 —
// the default conflict_dup_threshold — so it pins the >= inclusion at
// the ruling boundary (0.60 in, anything below out):
//   - dup: identical rows -> multiset 1.0 -> listed
//   - boundary: topic "a", summaries "aabc"/"aaxy" -> exactly 0.6
//   - below: topic "a", summary "zzzz" vs "aabc" -> 0.2 -> excluded
const (
	mergeAuditBoundaryTopic = "a"
	mergeAuditSummaryA      = "aabc"
	mergeAuditSummaryB      = "aaxy"
	mergeAuditSummaryLow    = "zzzz"
)

// mergeAuditRowsFixture — the standard fake scan rows (id, topic, summary).
func mergeAuditRowsFixture() []MergeAuditRow {
	return []MergeAuditRow{
		{ID: 1, Topic: echoTestTopic, Summary: echoDupSummary},
		{ID: 2, Topic: echoTestTopic, Summary: echoDupSummary},
		{ID: 3, Topic: mergeAuditBoundaryTopic, Summary: mergeAuditSummaryA},
		{ID: 4, Topic: mergeAuditBoundaryTopic, Summary: mergeAuditSummaryB},
		{ID: 5, Topic: mergeAuditBoundaryTopic, Summary: mergeAuditSummaryLow},
	}
}

// TestMergeAuditBoundaryScoreIsExact06 — the boundary fixture really is
// 0.6 by the single scoring definition (BigramDiceMultiset over the
// combined topic+" "+summary texts) — the boundary test below inherits
// its exactness from here.
func TestMergeAuditBoundaryScoreIsExact06(t *testing.T) {
	got := query.BigramDiceMultiset(
		mergeAuditBoundaryTopic+" "+mergeAuditSummaryA,
		mergeAuditBoundaryTopic+" "+mergeAuditSummaryB)
	if math.Abs(got-0.6) > 1e-9 {
		t.Fatalf("경계 점수 = %v, want 정확히 0.6", got)
	}
	low := query.BigramDiceMultiset(
		mergeAuditBoundaryTopic+" "+mergeAuditSummaryA,
		mergeAuditBoundaryTopic+" "+mergeAuditSummaryLow)
	if low >= 0.6 {
		t.Fatalf("저점 점수 = %v, want < 0.6", low)
	}
}

// TestMergeAuditPairsListSimilarSynthetic — the synthetic similar pair
// (identical rows, Dice 1.0) and the exact-0.6 boundary pair are listed
// at the default conflict_dup_threshold (>= inclusion), the below-pair is
// excluded, and the listing sorts score-descending with ascending (a,b)
// tie-break. The echo duplicates classification is reused (relation
// "duplicates" — the EchoScan ruling: at or above the threshold is the
// duplicate class).
func TestMergeAuditPairsListSimilarSynthetic(t *testing.T) {
	pairs, _ := ClassifyMergeAuditPairs(mergeAuditRowsFixture(),
		store.DefaultThresholds().ConflictDupThreshold)
	if len(pairs) != 2 {
		t.Fatalf("쌍 %d건, want 2(동일 1.0 + 경계 0.6): %+v", len(pairs), pairs)
	}
	if pairs[0].A != 1 || pairs[0].B != 2 {
		t.Errorf("첫 쌍 = (%d,%d), want (1,2)(1.0 동일 행)", pairs[0].A, pairs[0].B)
	}
	if math.Abs(pairs[0].Score-1.0) > 1e-9 || pairs[0].Relation != EchoRelationDuplicates {
		t.Errorf("첫 쌍 score/relation = %v/%q, want 1.0/duplicates", pairs[0].Score, pairs[0].Relation)
	}
	if pairs[1].A != 3 || pairs[1].B != 4 {
		t.Errorf("경계 쌍 = (%d,%d), want (3,4)", pairs[1].A, pairs[1].B)
	}
	if math.Abs(pairs[1].Score-0.6) > 1e-9 {
		t.Errorf("경계 쌍 score = %v, want 0.6", pairs[1].Score)
	}
	if pairs[1].TopicA != mergeAuditBoundaryTopic || pairs[1].TopicB != mergeAuditBoundaryTopic {
		t.Errorf("경계 쌍 토픽 = %q/%q, want 원 토픽", pairs[1].TopicA, pairs[1].TopicB)
	}
}

// TestMergeAuditThresholdBoundaryExactness — the >= inclusion boundary:
// with dupThreshold exactly equal to a pair's score the pair stays
// listed; a hair above (the 0.59/0.60 class — anything below the
// threshold is out) drops it.
func TestMergeAuditThresholdBoundaryExactness(t *testing.T) {
	rows := []MergeAuditRow{
		{ID: 3, Topic: mergeAuditBoundaryTopic, Summary: mergeAuditSummaryA},
		{ID: 4, Topic: mergeAuditBoundaryTopic, Summary: mergeAuditSummaryB},
	}
	at, _ := ClassifyMergeAuditPairs(rows, 0.6)
	if len(at) != 1 {
		t.Fatalf("임계 0.6(경계 점수 0.6) 쌍 %d건, want 1(>= 포함)", len(at))
	}
	above, _ := ClassifyMergeAuditPairs(rows, 0.6+1e-9)
	if len(above) != 0 {
		t.Fatalf("임계 0.6+ε 쌍 %d건, want 0(임계 미만 배제)", len(above))
	}
}

// TestMergeAuditFetchScanCap — the bounded fetch: LIMIT is scan_cap+1
// (one lookahead row names truncation), the active filter
// (invalid_at IS NULL) rides the query, and a result longer than the cap
// is truncated with the flag set.
func TestMergeAuditFetchScanCap(t *testing.T) {
	var rows [][]any
	for i := 1; i <= 5; i++ {
		rows = append(rows, []any{int64(i), echoTestTopic, echoDupSummary})
	}
	st := &fakeStore{rows: rows}
	got, truncated, err := FetchMergeAuditRows(context.Background(), st, 3)
	if err != nil {
		t.Fatalf("FetchMergeAuditRows 에러: %v", err)
	}
	if len(got) != 3 || !truncated {
		t.Fatalf("스캔 행 %d건 truncated=%v, want 3/true(상한 초과 절단)", len(got), truncated)
	}
	joined := strings.Join(st.queries, ";")
	if !strings.Contains(joined, "invalid_at IS NULL") {
		t.Errorf("스캔 SQL에 active 필터 부재: %q", joined)
	}
	if !strings.Contains(joined, "LIMIT 4") {
		t.Errorf("스캔 SQL에 LIMIT 4(상한 3+1) 부재: %q", joined)
	}
	// exactly at the cap — no truncation flag.
	st2 := &fakeStore{rows: rows[:3]}
	got2, truncated2, err := FetchMergeAuditRows(context.Background(), st2, 3)
	if err != nil {
		t.Fatalf("FetchMergeAuditRows(경계) 에러: %v", err)
	}
	if len(got2) != 3 || truncated2 {
		t.Fatalf("경계 스캔 행 %d건 truncated=%v, want 3/false", len(got2), truncated2)
	}
}

// TestMergeAuditFetchErrorPropagates — a scan failure returns the error
// (the caller maps the exit, the EchoScan convention).
func TestMergeAuditFetchErrorPropagates(t *testing.T) {
	st := &fakeStore{queryErr: errors.New("boom")}
	if _, _, err := FetchMergeAuditRows(context.Background(), st, 5); err == nil {
		t.Fatal("오류 미전파")
	}
}

// TestMergeAuditCostEstimateFields — the pre-cost estimate publication:
// pairs_total is min(cap,n)×(min(cap,n)−1)/2 over the scanned rows (here
// n=5 -> 10), micros_per_pair is the measured (calibration, ceil —
// conservative) per-pair cost, estimated_micros = per×total, and the
// in-budget scan keeps every row (no advice).
func TestMergeAuditCostEstimateFields(t *testing.T) {
	rows := mergeAuditRowsFixture()
	_, cost := ClassifyMergeAuditPairs(rows, 0.6)
	if cost.PairsTotal != 10 {
		t.Errorf("pairs_total = %d, want 10(5×4/2)", cost.PairsTotal)
	}
	if cost.MicrosPerPair <= 0 {
		t.Errorf("micros_per_pair = %d, want > 0(실측 ceil)", cost.MicrosPerPair)
	}
	if cost.EstimatedMicros != cost.MicrosPerPair*cost.PairsTotal {
		t.Errorf("estimated_micros = %d, want per×total=%d",
			cost.EstimatedMicros, cost.MicrosPerPair*cost.PairsTotal)
	}
	if cost.BudgetMicros != MergeAuditBudgetMicros {
		t.Errorf("budget_micros = %d, want 코드 상수 %d", cost.BudgetMicros, MergeAuditBudgetMicros)
	}
	if cost.OverBudget || cost.AdvisedCap != 0 || cost.EffectiveRows != len(rows) {
		t.Errorf("예산 내 스캔 cost = %+v, want over=false·무안내·전 행 스캔", cost)
	}
}

// TestMergeAuditOverBudgetCapReduction — a budget the estimate exceeds
// truncates the scan to the largest affordable row count (the O(n²)
// defense): the cost flags over_budget, names the advised cap, and a
// pair spanning dropped rows never lists.
func TestMergeAuditOverBudgetCapReduction(t *testing.T) {
	saved := MergeAuditBudgetMicros
	MergeAuditBudgetMicros = 1 // 1µs — any multi-row scan exceeds it
	t.Cleanup(func() { MergeAuditBudgetMicros = saved })

	rows := mergeAuditRowsFixture() // the (1,2) identical pair is rows[0],[1]
	pairs, cost := ClassifyMergeAuditPairs(rows, 0.6)
	if !cost.OverBudget {
		t.Fatalf("over_budget = false, want true(예산 초과): %+v", cost)
	}
	if cost.AdvisedCap <= 0 || cost.AdvisedCap >= len(rows) {
		t.Errorf("advised_cap = %d, want 0 < cap < %d(감소 안내)", cost.AdvisedCap, len(rows))
	}
	if cost.EffectiveRows != cost.AdvisedCap {
		t.Errorf("effective_rows = %d, want advised_cap=%d", cost.EffectiveRows, cost.AdvisedCap)
	}
	for _, p := range pairs {
		if p.A > cost.AdvisedCap || p.B > cost.AdvisedCap {
			t.Errorf("감소 상한 밖 쌍 (%d,%d) 리스팅 — want 미스캔", p.A, p.B)
		}
	}
}

// TestMergeAuditEmptyAndSingleRow — 0/1 rows cost nothing (pairs_total
// 0, no division hazard) and list nothing.
func TestMergeAuditEmptyAndSingleRow(t *testing.T) {
	for _, rows := range [][]MergeAuditRow{nil, {{ID: 1, Topic: "a", Summary: "b"}}} {
		pairs, cost := ClassifyMergeAuditPairs(rows, 0.6)
		if len(pairs) != 0 || cost.PairsTotal != 0 {
			t.Errorf("행 %d건: 쌍 %d·pairs_total %d, want 0/0", len(rows), len(pairs), cost.PairsTotal)
		}
	}
}
