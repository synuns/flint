// status_v0180_test.go — status's windowed read metrics. Alongside the cumulative
// read_zero_pct (dominated by early history), a 14-day daily-sum window value is reported.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// windowedReadHarness — a fake returning 3 keys for daily:* queries (today, yesterday, and 40 days ago — outside the window).
// Excluding the outside-window sum is the core contract, so extreme values (100/100) are seeded.
func windowedReadHarness(t *testing.T) *fakeStore {
	t.Helper()
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -40).Format("2006-01-02")
	daily := `{"read":10,"read_zero":4,"read_zero_near":1}` // today
	dailyY := `{"read":10,"read_zero":1}`                   // yesterday
	dailyOld := `{"read":100,"read_zero":100}`              // outside the window
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "LIKE 'daily:%'") {
			return [][]any{
				{"daily:" + today, daily},
				{"daily:" + yesterday, dailyY},
				{"daily:" + old, dailyOld},
			}, nil
		}
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"5"}}, nil // a schema_version compatibility value — the corrupt-JSON path falls back to 0
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	return f
}

// TestStatusWindowedRead — only the in-window (14-day) daily sums: read=20, zero=5 ->
// zero_pct 25. The extreme 40-day-old values (100/100) are excluded.
func TestStatusWindowedRead(t *testing.T) {
	f := windowedReadHarness(t)
	read, zeroPct, nearPct := statusWindowedRead(context.Background(), f, 14)
	if read != 20 {
		t.Fatalf("read=%d, want 20(창 내 합산)", read)
	}
	if zeroPct != 25 {
		t.Fatalf("zeroPct=%d, want 25(5/20)", zeroPct)
	}
	if nearPct != 5 {
		t.Fatalf("nearPct=%d, want 5(1/20)", nearPct)
	}
}

// TestStatusWindowedKeys — read_calls_14d and read_zero_pct_14d keys exist additively in the
// status output with the window-sum values (existing keys unchanged).
func TestStatusWindowedKeys(t *testing.T) {
	windowedReadHarness(t)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if v, ok := m["read_calls_14d"].(float64); !ok || int(v) != 20 {
		t.Fatalf("read_calls_14d=%v, want 20", m["read_calls_14d"])
	}
	if v, ok := m["read_zero_pct_14d"].(float64); !ok || int(v) != 25 {
		t.Fatalf("read_zero_pct_14d=%v, want 25", m["read_zero_pct_14d"])
	}
	for _, legacy := range []string{"read_calls", "read_zero_pct", "pending", "schema_version"} {
		if _, ok := m[legacy]; !ok {
			t.Errorf("기존 키 %s 누락 — additive 원칙 위반", legacy)
		}
	}
}

// TestStatusZeroNearKey — exposes read_zero_near_pct_14d (surfacing the near-fallback).
func TestStatusZeroNearKey(t *testing.T) {
	windowedReadHarness(t)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	m := decode(t, out)
	if v, ok := m["read_zero_near_pct_14d"].(float64); !ok || int(v) != 5 {
		t.Fatalf("read_zero_near_pct_14d=%v, want 5(1/20)", m["read_zero_near_pct_14d"])
	}
}
