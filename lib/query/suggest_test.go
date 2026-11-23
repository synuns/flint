package query

import (
	"math"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func resetSuggestThresholds(t *testing.T) {
	t.Helper()
	def := store.DefaultThresholds()
	SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	t.Cleanup(func() { SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap) })
}

func scanMaxRows() [][]any {
	return [][]any{
		{int32(1), "abxx", ""},
		{int32(2), "abqz", ""},
		{int32(3), "zzzz", ""},
	}
}

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
