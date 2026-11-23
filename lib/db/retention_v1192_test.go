package db

import (
	"testing"
	"time"
)

func TestDaysCutoffClampsCeiling(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		days int
		want time.Time
	}{
		{"200000 — 상한 클램프", 200000, now.AddDate(0, 0, -106751)},
		{"106751 — 상한 그대로", 106751, now.AddDate(0, 0, -106751)},
		{"음수 — 0", -5, now},
		{"0 — 0", 0, now},
		{"90 — 통상", 90, now.AddDate(0, 0, -90)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DaysCutoff(tc.days, now); !got.Equal(tc.want) {
				t.Fatalf("DaysCutoff(%d) = %v, want %v", tc.days, got, tc.want)
			}
		})
	}
}

func TestDaysCutoffNeverFuture(t *testing.T) {
	now := time.Now()
	for _, days := range []int{0, 1, 90, 106751, 106752, 200000, 1 << 20, -1} {
		if got := DaysCutoff(days, now); got.After(now) {
			t.Fatalf("DaysCutoff(%d) = %v — 미래 컷오프(래브 결함)", days, got)
		}
	}
}

func TestDaysSpanNeverNegative(t *testing.T) {
	now := time.Now()
	got := DaysSpan(200000, now)
	if got <= 0 {
		t.Fatalf("DaysSpan(200000) = %v, want 양수(클램프)", got)
	}
	want := 106751 * 24 * time.Hour
	if got < want-time.Hour || got > want+time.Hour {
		t.Fatalf("DaysSpan(200000) = %v, want ~%v(상한 클램프)", got, want)
	}
	if s := DaysSpan(7, now); s < 167*time.Hour || s > 169*time.Hour {
		t.Fatalf("DaysSpan(7) = %v, want ~168h", s)
	}
}

func TestRetentionCutoffDaysDelegates(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if got := retentionCutoffDays(0, 90, now); !got.Equal(now.AddDate(0, 0, -90)) {
		t.Fatalf("retentionCutoffDays(0,90) = %v, want 디폴트 폴백", got)
	}
	if got := retentionCutoffDays(-1, 90, now); !got.Equal(now.AddDate(0, 0, -90)) {
		t.Fatalf("retentionCutoffDays(-1,90) = %v, want 디폴트 폴백", got)
	}
	if got := retentionCutoffDays(200000, 90, now); !got.Equal(now.AddDate(0, 0, -106751)) {
		t.Fatalf("retentionCutoffDays(200000,90) = %v, want 상한 클램프", got)
	}
}
