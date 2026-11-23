package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func metaRowsFn(keys map[string]string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		var rows [][]any
		for k, v := range keys {
			rows = append(rows, []any{k, v})
		}
		return rows, nil
	}
}

func TestOldestDailyFlatKey(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: metaRowsFn(map[string]string{
		"daily:write:2026-01-01": "7",
		"daily:hot:2026-02-01":   "1",
		"daily:2025-12-01":       "3",
		"last_write_at":          "2026-11-01T00:00:00Z",
	})}}
	out, code := runMaintainArgs(t, f, "--meta-audit")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if !strings.Contains(out, `"oldest_daily":"daily:2025-12-01"`) {
		t.Errorf("oldest_daily 플랫 키 미탐/오산: %s", out)
	}
	if !strings.Contains(out, `"daily":3`) {
		t.Errorf("daily 카운트 = 3 아님: %s", out)
	}
}

func TestOldestDailyFlatKeyOldest(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: metaRowsFn(map[string]string{
		"daily:write:2025-11-15": "2",
		"daily:hot:2026-03-01":   "1",
		"daily:write:notadate":   "9",
	})}}
	out, code := runMaintainArgs(t, f, "--meta-audit")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if !strings.Contains(out, `"oldest_daily":"daily:write:2025-11-15"`) {
		t.Errorf("oldest_daily가 날짜 기준 아님(무효 날짜 포함 가능): %s", out)
	}
	if !strings.Contains(out, `"daily":3`) {
		t.Errorf("무효 날짜 키도 daily 카운트에 포함되어야 함: %s", out)
	}
}

func TestStaleDailyDetectsFlatKeys(t *testing.T) {
	days := int(time.Since(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if days <= 7 {
		t.Skip("컷오프 2026-06-01 도달 불가한 시계")
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		return [][]any{
			{"daily:write:2026-01-01"},
			{"daily:2026-07-01"},
			{"daily:write:notadate"},
			{"last_write_at"},
		}, nil
	}}

	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })
	c := checkMetaHygieneTh(context.Background(), f, store.Thresholds{DailyRetentionD: days - 7})
	if c.Status != "warn" {
		t.Fatalf("stale 플랫 키 미감지 = %+v", c)
	}
	if !strings.Contains(c.Detail, "1 daily counters past retention window") {
		t.Errorf("stale 카운트 = 1 아님(플랫 키 미탐 또는 무효 날짜 포함): %+v", c)
	}
	if strings.Contains(c.Detail, "unknown keys") {
		t.Errorf("last_write_at이 미지 키로 오탐: %+v", c)
	}
}
