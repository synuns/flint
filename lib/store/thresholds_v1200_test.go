package store

import "testing"

func TestThresholdsV1200DiagDefaults(t *testing.T) {
	th := DefaultThresholds()
	want := []struct {
		name string
		got  any
		want any
	}{
		{"DiagHotBudgetHigh", th.DiagHotBudgetHigh, 0.8},
		{"DiagHotBudgetLow", th.DiagHotBudgetLow, 0.2},
		{"DiagTrendPct", th.DiagTrendPct, 50},
		{"DiagRecallLedGoal", th.DiagRecallLedGoal, 50},
		{"DiagDeadWarn", th.DiagDeadWarn, 10},
		{"KreadsRetentionD", th.KreadsRetentionD, 14},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("%s = %v, want %v", w.name, w.got, w.want)
		}
	}
}

func TestThresholdsV1200DiagParseOverride(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"diag_hot_budget_high", "0.7"},
		{"diag_hot_budget_low", "0.1"},
		{"diag_trend_pct", "80"},
		{"diag_recall_led_goal", "40"},
		{"diag_dead_warn", "20"},
		{"knowledge_reads_retention_days", "30"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.DiagHotBudgetHigh != 0.7 || th.DiagHotBudgetLow != 0.1 {
		t.Errorf("float 오버라이드 미반영: %v/%v", th.DiagHotBudgetHigh, th.DiagHotBudgetLow)
	}
	if th.DiagTrendPct != 80 || th.DiagRecallLedGoal != 40 ||
		th.DiagDeadWarn != 20 || th.KreadsRetentionD != 30 {
		t.Errorf("int 오버라이드 미반영: %+v", th)
	}
}

func TestThresholdsV1200DiagInvalidFallback(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"diag_hot_budget_high", "1.5"},
		{"diag_hot_budget_low", "abc"},
		{"diag_trend_pct", "-10"},
		{"diag_recall_led_goal", "x"},
		{"diag_dead_warn", "-1"},
		{"knowledge_reads_retention_days", "0일"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.DiagHotBudgetHigh != 0.8 || th.DiagHotBudgetLow != 0.2 {
		t.Errorf("무효 float 기본값 폴백 아님: %v/%v", th.DiagHotBudgetHigh, th.DiagHotBudgetLow)
	}
	if th.DiagTrendPct != 50 || th.DiagRecallLedGoal != 50 ||
		th.DiagDeadWarn != 10 || th.KreadsRetentionD != 14 {
		t.Errorf("무효 int 기본값 폴백 아님: %+v", th)
	}
}
