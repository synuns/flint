// retention_v1193_test.go — the v1.19.3 hour/minute-unit span clamp
// regressions (R77-R78): HoursSpan/MinutesSpan clamp a grace/window/interval
// override past the Duration ceiling to the 87600h (10-year) ceiling — the
// v1.19.1 metering_freshness_warn_h precedent — instead of wrapping
// time.Duration(h)*time.Hour negative (a wrapped span flips every elapsed
// comparison: always-stall, always-due, never-chain).
package db

import (
	"testing"
	"time"
)

func TestHoursSpanClampsCeiling(t *testing.T) {
	cases := []struct {
		name string
		h    int
		want time.Duration
	}{
		{"3000000 — 상한 클램프", 3000000, 87600 * time.Hour},
		{"87600 — 상한 그대로", 87600, 87600 * time.Hour},
		{"48 — 통상", 48, 48 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HoursSpan(tc.h, 72); got != tc.want {
				t.Fatalf("HoursSpan(%d,72) = %v, want %v", tc.h, got, tc.want)
			}
		})
	}
}

// TestHoursSpanFallback — h <= 0 falls back to def (the ApplyThresholdsFallback
// convention); a non-positive def stays 0 (never negative — a negative grace
// warned on every heartbeat).
func TestHoursSpanFallback(t *testing.T) {
	if got := HoursSpan(0, 72); got != 72*time.Hour {
		t.Fatalf("HoursSpan(0,72) = %v, want 72h(def 폴백)", got)
	}
	if got := HoursSpan(-5, 72); got != 72*time.Hour {
		t.Fatalf("HoursSpan(-5,72) = %v, want 72h(음수 폴백)", got)
	}
	if got := HoursSpan(0, 0); got != 0 {
		t.Fatalf("HoursSpan(0,0) = %v, want 0(음수 스팬 금지)", got)
	}
}

// TestMinutesSpanClampsCeiling — the minute-unit twin: the ceiling equals
// 87600h expressed in minutes; normal values pass through unchanged.
func TestMinutesSpanClampsCeiling(t *testing.T) {
	if got := MinutesSpan(3000000*60, 30); got != 87600*time.Hour {
		t.Fatalf("MinutesSpan(과대) = %v, want 87600h(상한 클램프)", got)
	}
	if got := MinutesSpan(30, 45); got != 30*time.Minute {
		t.Fatalf("MinutesSpan(30,45) = %v, want 30m(통상 불변)", got)
	}
	if got := MinutesSpan(0, 45); got != 45*time.Minute {
		t.Fatalf("MinutesSpan(0,45) = %v, want 45m(def 폴백)", got)
	}
}
