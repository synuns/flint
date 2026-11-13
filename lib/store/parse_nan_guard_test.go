package store

import "testing"

// TestParseFloatNaNInfGuard — v1.2.2(R16-B1): NaN·±Inf는 파싱 불가로
// 기본값 폴백 — rrf_k=NaN이 RRF 점수 전체를 조용히 무의미화하는 것 차단.
func TestParseFloatNaNInfGuard(t *testing.T) {
	th, _ := ParseThresholds([][2]any{{"rrf_k", "NaN"}})
	if !th.RrfKMatchesDefault() {
		t.Fatalf("rrf_k=NaN → %v, want 기본값(폴백)", th.RrfK)
	}
	th2, _ := ParseThresholds([][2]any{{"rrf_k", "+Inf"}})
	if !th2.RrfKMatchesDefault() {
		t.Fatalf("rrf_k=+Inf → %v, want 기본값(폴백)", th2.RrfK)
	}
	// parseFloatOrDefault(0~1 파서)도 NaN 폴백
	th3, _ := ParseThresholds([][2]any{{"conf_floor", "NaN"}})
	if th3.ConfFloor != 0.8 {
		t.Fatalf("conf_floor=NaN → %v, want 기본값 0.8(폴백)", th3.ConfFloor)
	}
	// 정상값 통과 회귀
	th4, _ := ParseThresholds([][2]any{{"rrf_k", "30"}})
	if th4.RrfK != 30 {
		t.Fatalf("rrf_k=30 → %v(정상값 회귀)", th4.RrfK)
	}
}

// RrfKMatchesDefault — 테스트 보조(기본값 비교).
func (th Thresholds) RrfKMatchesDefault() bool {
	return th.RrfK == 60.0
}
