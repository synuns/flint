// suggest_v0163_test.go — the suggestion-threshold seam. The defaults come from
// store.DefaultThresholds (the registry's single source); at runtime, cli injects
// the meta override values via SetSuggestThresholds.
package query

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// that the defaults share the source with the registry (thresholds.go) — preventing a hardcoded dual source.
func TestSuggestDefaultsFromThresholds(t *testing.T) {
	def := store.DefaultThresholds()
	if suggestMinScore != def.SuggestMinScore {
		t.Errorf("suggestMinScore = %v, want %v(thresholds 기본값)", suggestMinScore, def.SuggestMinScore)
	}
	if suggestScanCap != def.SuggestScanCap {
		t.Errorf("suggestScanCap = %d, want %d(thresholds 기본값)", suggestScanCap, def.SuggestScanCap)
	}
}

// SetSuggestThresholds — the injected minScore and scanCap are reflected in the
// suggestion filter and the scan SQL (the meta-override priority path).
func TestSetSuggestThresholdsApplies(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	SetSuggestThresholds(0.99, 7)

	sql, _ := BuildSuggestScan()
	m := regexp.MustCompile(`LIMIT (\d+)`).FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("스캔 SQL에 LIMIT 없음: %q", sql)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n != 7 {
		t.Errorf("스캔 상한 = %s, want 7(오버라이드)", m[1])
	}

	rows := [][]any{
		{int32(1), "flint 백업 절차", ""}, // 질의와 동일 → Dice 1.0(상한 통과)
		{int32(2), "전혀 다른 주제", "무관 요약"},
	}
	got := SuggestSimilar("flint 백업 절차", rows, 3)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("minScore 0.99 필터 = %+v, want 동일 행 1건만", got)
	}
	if !strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
		t.Errorf("스캔 SQL 형태 변형: %q", sql)
	}
}

// injections <= 0 are ignored (double defense against unset fallbacks).
func TestSetSuggestThresholdsIgnoresNonPositive(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	before := store.DefaultThresholds()
	SetSuggestThresholds(0, 0)
	if suggestMinScore != before.SuggestMinScore || suggestScanCap != before.SuggestScanCap {
		t.Errorf("0 주입이 값을 덮어씀: min=%v cap=%d", suggestMinScore, suggestScanCap)
	}
	SetSuggestThresholds(-0.5, -1)
	if suggestMinScore != before.SuggestMinScore || suggestScanCap != before.SuggestScanCap {
		t.Errorf("음수 주입이 값을 덮어씀: min=%v cap=%d", suggestMinScore, suggestScanCap)
	}
}
