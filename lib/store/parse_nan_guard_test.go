package store

import "testing"

// TestParseFloatNaNInfGuard — NaN and ±Inf are treated as unparsable and fall
// back to defaults — blocking rrf_k=NaN from silently rendering the whole RRF score meaningless.
func TestParseFloatNaNInfGuard(t *testing.T) {
	th, _ := ParseThresholds([][2]any{{"rrf_k", "NaN"}})
	if !th.RrfKMatchesDefault() {
		t.Fatalf("rrf_k=NaN → %v, want 기본값(폴백)", th.RrfK)
	}
	th2, _ := ParseThresholds([][2]any{{"rrf_k", "+Inf"}})
	if !th2.RrfKMatchesDefault() {
		t.Fatalf("rrf_k=+Inf → %v, want 기본값(폴백)", th2.RrfK)
	}
	// parseFloatOrDefault (the 0..1 parser) also falls back on NaN
	th3, _ := ParseThresholds([][2]any{{"conf_floor", "NaN"}})
	if th3.ConfFloor != 0.8 {
		t.Fatalf("conf_floor=NaN → %v, want 기본값 0.8(폴백)", th3.ConfFloor)
	}
	// a normal-value pass regression
	th4, _ := ParseThresholds([][2]any{{"rrf_k", "30"}})
	if th4.RrfK != 30 {
		t.Fatalf("rrf_k=30 → %v(정상값 회귀)", th4.RrfK)
	}
}

// RrfKMatchesDefault — a test helper (default comparison).
func (th Thresholds) RrfKMatchesDefault() bool {
	return th.RrfK == 60.0
}
