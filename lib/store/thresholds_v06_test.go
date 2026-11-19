package store

import "testing"

// the 3 new thresholds' defaults — stale_days 90, stall_hot_h 48, stall_write_h 72.
func TestThresholdsV06Defaults(t *testing.T) {
	th := DefaultThresholds()
	if th.StaleDays != 90 {
		t.Errorf("StaleDays=%d, want 90", th.StaleDays)
	}
	if th.StallHotH != 48 {
		t.Errorf("StallHotH=%d, want 48", th.StallHotH)
	}
	if th.StallWriteH != 72 {
		t.Errorf("StallWriteH=%d, want 72", th.StallWriteH)
	}
}

// meta overrides — e.g. ParseThresholds(+[["stale_days","30"]]) -> StaleDays=30.
func TestThresholdsV06ParseOverride(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"stale_days", "30"},
		{"stall_hot_h", "6"},
		{"stall_write_h", "12"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.StaleDays != 30 || th.StallHotH != 6 || th.StallWriteH != 12 {
		t.Errorf("override 미반영: %+v", th)
	}
}

// invalid values (non-numeric, negative, fractional) fall back to defaults (the global constraint).
func TestThresholdsV06InvalidFallback(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"stale_days", "abc"},
		{"stall_hot_h", "-1"},
		{"stall_write_h", "1.5"},
	})
	if err != nil {
		t.Fatalf("무효값은 에러가 아니다: %v", err)
	}
	if th.StaleDays != 90 || th.StallHotH != 48 || th.StallWriteH != 72 {
		t.Errorf("기본값 폴백 실패: %+v", th)
	}
}

// existing and new keys coexist without interfering with each other.
func TestThresholdsV06CoexistWithLegacyKeys(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"pending_max", "10"},
		{"stale_days", "7"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.PendingMax != 10 || th.DupSuspMax != 20 {
		t.Errorf("기존 키 회귀: %+v", th)
	}
	if th.StaleDays != 7 {
		t.Errorf("StaleDays=%d, want 7", th.StaleDays)
	}
}

// unset new keys stay 0 (unset) — the consumer (loadThresholds) falls back to DefaultThresholds
// (the existing whole-struct comparison-test compatibility convention — see the thresholds.go comment).
func TestThresholdsV06UnsetStaysZero(t *testing.T) {
	th, err := ParseThresholds(nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.StaleDays != 0 || th.StallHotH != 0 || th.StallWriteH != 0 {
		t.Errorf("미설정 신규 키는 0이어야 함: %+v", th)
	}
}
