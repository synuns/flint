package store

import "testing"

func TestParseRecallWarnWritesOff(t *testing.T) {
	cases := []struct {
		val  string
		want int
	}{
		{"-1", -1},
		{"5", 5},
		{"0", 0},
		{"x", 3},
		{"-2", 3},
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

	th2, _ := ParseThresholds([][2]any{{"dead_days", "many"}})
	if th2.DeadDays != 30 {
		t.Errorf("무효 dead_days = %d, want 기본 30", th2.DeadDays)
	}
}
