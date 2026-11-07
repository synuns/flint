// thresholds_v0181_test.go — v0.18.1: recall_warn_writes의 -1(끔) 의미론.
// 공유 파서는 음수를 기본값으로 코안다려 이 키만 -1까지 수용한다.
package store

import "testing"

func TestParseRecallWarnWritesOff(t *testing.T) {
	cases := []struct {
		val  string
		want int
	}{
		{"-1", -1}, // 끔
		{"5", 5},   // 정상 오버라이드
		{"0", 0},   // 명시 0 — 미설정과 동일 취급(소비자가 기본값 폴백)
		{"x", 3},   // 파싱 불가 → 기본값
		{"-2", 3},  // -1 미만 → 기본값
	}
	for _, tc := range cases {
		th, err := ParseThresholds([][2]any{{"recall_warn_writes", tc.val}})
		if err != nil {
			t.Fatalf("ParseThresholds(%q): %v", tc.val, err)
		}
		if th.RecallWarnWrites != tc.want {
			t.Errorf("recall_warn_writes=%q → %d, want %d", tc.val, th.RecallWarnWrites, tc.want)
		}
	}
}

// TestParseV0204EffectWindows — v0.20.4(패치 루프 4차): 효과 계측 판정
// 창·보존 키 파싱 — 유효 값 수용·무효 값 기본 폴백.
func TestParseV0204EffectWindows(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"dead_days", "60"}, {"pair_window_minutes", "15"},
		{"chain_window_hours", "48"}, {"events_retention_days", "7"},
		{"hot_events_retention_days", "90"}, {"events_volume_warn", "1000"},
		{"dead_days_bogus", "x"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds: %v", err)
	}
	if th.DeadDays != 60 || th.PairWindowMin != 15 || th.ChainWindowH != 48 ||
		th.EventsRetentionD != 7 || th.HotRetentionD != 90 || th.EventsVolumeWarn != 1000 {
		t.Errorf("효과 창 = %+v", th)
	}
	// 무효 값은 기본값.
	th2, _ := ParseThresholds([][2]any{{"dead_days", "many"}})
	if th2.DeadDays != 30 {
		t.Errorf("무효 dead_days = %d, want 기본 30", th2.DeadDays)
	}
}
