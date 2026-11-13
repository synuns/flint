// daily_age_test.go — v1.2.3 T3(R17 A3): daily 카운터 나이 판정의 플랫
// 키(daily:<kind>:<date>) 지원. 기존 TrimPrefix 문자열 비교는 'w'>'2'라
// 플랫 키 전체를 미탐 — maintain oldest-daily·doctor staleDaily 모두
// 마지막 세그먼트 날짜로 판정하는지 검증. 예시는 합성 데모 날짜.
package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

// metaRowsFn — maintain --meta-audit용 meta (key,value) 조회 fake.
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

// TestOldestDailyFlatKey — 플랫 키 혼재 시 oldest-daily가 날짜 기준으로
// 산출되는지(출력 형식은 기존 그대로 — 키 원문).
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

// TestOldestDailyFlatKeyOldest — 날짜 비교가 키 문자열 비교가 아닌지:
// lexicographic로는 daily:hot:…가 먼저지만 날짜상 daily:write:2025-11-15가
// oldest. 무효 날짜 세그먼트(daily:write:notadate)는 나이 판정에서 제외.
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

// TestStaleDailyDetectsFlatKeys — doctor meta-hygiene의 stale 판정이
// 플랫 키의 마지막 세그먼트 날짜로 동작하는지. 컷오프 2026-06-01.
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
	// doctor_test.go seam이 항상 false로 고정되어 있어 실제 판정으로 교체
	// (반환 후 원복) — 비-daily 키 제외 로직 불변 확인용.
	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })
	c := checkMetaHygieneTh(context.Background(), f, store.Thresholds{DailyRetentionD: days - 7})
	if c.Status != "warn" {
		t.Fatalf("stale 플랫 키 미감지 = %+v", c)
	}
	if !strings.Contains(c.Detail, "daily 카운터 1건") {
		t.Errorf("stale 카운트 = 1 아님(플랫 키 미탐 또는 무효 날짜 포함): %+v", c)
	}
	if strings.Contains(c.Detail, "미지 키") {
		t.Errorf("last_write_at이 미지 키로 오탐: %+v", c)
	}
}
