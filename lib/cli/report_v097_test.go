// report_v097_test.go — pinning the dupPct and fallbackPct pure functions
// (period duplication rate and fallback ratio; 0 for zero denominators).
package cli

import "testing"

func TestDupPctCalc(t *testing.T) {
	days := []DailyRow{{Date: "d1", Write: 10, Dup: 30}, {Date: "d2", Write: 0, Dup: 10}}
	if got := dupPct(days); got != 80 {
		t.Errorf("dupPct = %d, want 80", got)
	}
	if got := dupPct(nil); got != 0 {
		t.Errorf("빈 기간 = %d, want 0", got)
	}
}

func TestFallbackPctZeroDenominator(t *testing.T) {
	if got := fallbackPct(3, 16); got != 18 {
		t.Errorf("fallbackPct = %d, want 18", got)
	}
	if got := fallbackPct(0, 0); got != 0 {
		t.Errorf("제로 분모 = %d, want 0", got)
	}
}
