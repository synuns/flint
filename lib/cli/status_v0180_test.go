package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func windowedReadHarness(t *testing.T) *fakeStore {
	t.Helper()
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -40).Format("2006-01-02")
	daily := `{"read":10,"read_zero":4,"read_zero_near":1}`
	dailyY := `{"read":10,"read_zero":1}`
	dailyOld := `{"read":100,"read_zero":100}`
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
			return [][]any{{"5"}}, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	return f
}

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
