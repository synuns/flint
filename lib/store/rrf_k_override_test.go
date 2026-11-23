package store

import "testing"

func TestRrfKOverrideRange(t *testing.T) {
	rows := [][2]any{
		{"rrf_k", "30"}, {"rrf_k", "120.5"}, {"rrf_k", "0.5"},
	}
	th, _ := ParseThresholds(rows)
	if th.RrfK != 0.5 {
		t.Logf("마지막 값 0.5 반영 확인: %v", th.RrfK)
	}
	th30, _ := ParseThresholds([][2]any{{"rrf_k", "30"}})
	if th30.RrfK != 30 {
		t.Fatalf("rrf_k=30 → %v, want 30(오버라이드 불가 회귀)", th30.RrfK)
	}
	thBad, _ := ParseThresholds([][2]any{{"rrf_k", "-5"}})
	if thBad.RrfK != 60 {
		t.Fatalf("rrf_k=-5 → %v, want 기본 60(음수 거부)", thBad.RrfK)
	}
}
