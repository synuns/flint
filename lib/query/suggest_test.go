// suggest_test.go — the scan-max (schema v13 read_events.near_score)
// accrual contract: SuggestSimilarWithScanMax returns the highest
// bigramDice score over ALL scanned candidates (before the min_score
// filter — independent of the top-N selection), so a below-min zero row
// still carries its best scan score. ASCII fixtures only: NFKC is the
// identity on them, so the Dice values are hand-computable (q="abxy"
// bigrams {ab,bx,xy}: vs "abxx" {ab,bx,xx} → 2·2/6 = 2/3; vs "abqz"
// {ab,bq,qz} → 2·1/6 = 1/3; vs "zzzz" → 0).
package query

import (
	"math"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

// resetSuggestThresholds — restores the thresholds-registry defaults
// after a test overrides them via SetSuggestThresholds (the
// suggest_v0163_test save/restore convention).
func resetSuggestThresholds(t *testing.T) {
	t.Helper()
	def := store.DefaultThresholds()
	SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	t.Cleanup(func() { SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap) })
}

// scanMaxRows — the shared fixture: candidate scores 2/3·1/3·0 against
// q="abxy" (see the file header for the arithmetic).
func scanMaxRows() [][]any {
	return [][]any{
		{int32(1), "abxx", ""},
		{int32(2), "abqz", ""},
		{int32(3), "zzzz", ""},
	}
}

// TestSuggestSimilarScanMaxBelowMin — with min_score above every
// candidate score (0.9), no suggestion survives yet scanMax keeps the
// scan best (2/3 — the near=false zero row is NOT masked to 0; the
// min_score-boundary distribution is the counterfactual-analysis core).
func TestSuggestSimilarScanMaxBelowMin(t *testing.T) {
	resetSuggestThresholds(t)
	SetSuggestThresholds(0.9, 5000)
	sug, scanMax := SuggestSimilarWithScanMax("abxy", scanMaxRows(), 3)
	if len(sug) != 0 {
		t.Fatalf("suggestions = %v, want 0건(min 0.9 미만 전원 탈락)", sug)
	}
	if math.Abs(scanMax-2.0/3.0) > 1e-9 {
		t.Fatalf("scanMax = %v, want 2/3(스캔 최고치 — min_score 필터 이전)", scanMax)
	}
}

// TestSuggestSimilarScanMaxTopNIndependent — scanMax is the scan best
// regardless of the top-N truncation (topN=1 drops the 1/3 candidate
// from the output; scanMax still reflects the whole scan), and stays
// within 0..1 (the Dice range).
func TestSuggestSimilarScanMaxTopNIndependent(t *testing.T) {
	resetSuggestThresholds(t)
	SetSuggestThresholds(0.2, 5000)
	sug, scanMax := SuggestSimilarWithScanMax("abxy", scanMaxRows(), 1)
	if len(sug) != 1 || sug[0].ID != 1 {
		t.Fatalf("suggestions = %v, want 1건(id=1 — topN 절단)", sug)
	}
	if math.Abs(scanMax-2.0/3.0) > 1e-9 {
		t.Fatalf("scanMax = %v, want 2/3(topN과 무관 — 스캔 전체 최고치)", scanMax)
	}
	if scanMax < 0 || scanMax > 1 {
		t.Fatalf("scanMax = %v, want 0..1(Dice 범위)", scanMax)
	}
}

// TestSuggestSimilarScanMaxEmptyScan — an empty scan (0 candidate rows)
// yields no suggestions and scanMax 0 (nothing was scanned — the
// zero-of-empty case, distinct from a masked score).
func TestSuggestSimilarScanMaxEmptyScan(t *testing.T) {
	resetSuggestThresholds(t)
	sug, scanMax := SuggestSimilarWithScanMax("abxy", nil, 3)
	if len(sug) != 0 {
		t.Fatalf("suggestions = %v, want 0건(빈 스캔)", sug)
	}
	if scanMax != 0 {
		t.Fatalf("scanMax = %v, want 0(빈 스캔)", scanMax)
	}
}

// TestSuggestSimilarCompatDelegates — the legacy SuggestSimilar
// signature keeps its exact behavior (suggestions only) via delegation
// — the existing call sites compile and rank unchanged.
func TestSuggestSimilarCompatDelegates(t *testing.T) {
	resetSuggestThresholds(t)
	SetSuggestThresholds(0.2, 5000)
	got := SuggestSimilar("abxy", scanMaxRows(), 3)
	withMax, _ := SuggestSimilarWithScanMax("abxy", scanMaxRows(), 3)
	if len(got) != 2 || len(withMax) != 2 {
		t.Fatalf("suggestions = %d·%d건, want 2·2(min 0.2 — 위임 동일)", len(got), len(withMax))
	}
	for i := range got {
		if got[i] != withMax[i] {
			t.Fatalf("SuggestSimilar[%d] = %v, want %v(위임 결과 동일)", i, got[i], withMax[i])
		}
	}
}

// TestSuggestSimilarRecordsScanDuration — v1.21.0 A6: the wired
// RecordSuggestScanFn seam records exactly one non-negative duration per
// scan (SuggestSimilar's delegation records once — not twice), and the
// suggestions/scanMax stay identical with the seam on vs off (§13).
func TestSuggestSimilarRecordsScanDuration(t *testing.T) {
	resetSuggestThresholds(t)
	SetSuggestThresholds(0.2, 5000)

	var got []time.Duration
	RecordSuggestScanFn = func(d time.Duration) { got = append(got, d) }
	t.Cleanup(func() { RecordSuggestScanFn = nil })

	sugOn, maxOn := SuggestSimilarWithScanMax("abxy", scanMaxRows(), 3)
	if len(got) != 1 {
		t.Fatalf("기록 샘플 %d건, want 1(스캔 1회)", len(got))
	}
	if got[0] < 0 {
		t.Fatalf("기록 음수 duration: %v", got[0])
	}

	got = nil
	if legacy := SuggestSimilar("abxy", scanMaxRows(), 3); len(legacy) != 2 {
		t.Fatalf("legacy 위임 결과 %d건, want 2", len(legacy))
	}
	if len(got) != 1 {
		t.Fatalf("위임 경로 기록 %d건, want 1(이중 계측 금지)", len(got))
	}

	RecordSuggestScanFn = nil
	sugOff, maxOff := SuggestSimilarWithScanMax("abxy", scanMaxRows(), 3)
	if math.Abs(maxOn-maxOff) > 1e-9 || len(sugOn) != len(sugOff) {
		t.Fatalf("결과 변동: (%v, %d) vs (%v, %d)(계측이 결과에 끼면 안 됨)", maxOn, len(sugOn), maxOff, len(sugOff))
	}
	for i := range sugOn {
		if sugOn[i] != sugOff[i] {
			t.Fatalf("suggestion[%d] 변동: %v vs %v", i, sugOn[i], sugOff[i])
		}
	}
}
