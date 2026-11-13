package store

import "testing"

// TestRrfKOverrideRange — v1.2.1(R15-B1): rrf_k 오버라이드는 양수 전범위
// 허용 — 0~1 클램프 파서를 공유하면 기본값 60이 전부 기각되어 오버라이드가
// 불가했다(30 설정→60 복귀 실증).
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
