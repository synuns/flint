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
