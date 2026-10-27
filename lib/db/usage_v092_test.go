// usage_v092_test.go — v0.9.2 RecordUsageCount: dup N 일괄 증분(실DB slow).
// 계약: count 1회 호출이 daily 카운터에 N을 더한다(recordDups의 O(N)
// 읽기-쓰기 대체), dup는 last_ 시각 키 없음, count<1은 에러.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountBatches — RecordUsageCount("dup","",3)+("write","hook",2)
// 가 각 1회 upsert로 daily에 일괄 반영(회귀: N번 RecordUsage 루프).
func TestRecordUsageCountBatches(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	if err := r.RecordUsageCount(context.Background(), "dup", "", 3); err != nil {
		t.Fatalf("RecordUsageCount(dup,3): %v", err)
	}
	if err := r.RecordUsageCount(context.Background(), "write", "hook", 2); err != nil {
		t.Fatalf("RecordUsageCount(write,hook,2): %v", err)
	}

	got := dailyValue(t, r, today)
	want := map[string]int{"dup": 3, "write": 2, "hook": 2}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("daily:%s[%q] = %d, want %d (full=%v)", today, k, got[k], n, got)
		}
	}
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key = 'last_dup_at'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup_at: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup_at 존재 — dup은 last_ 시각을 기록하지 않는다")
	}
	if err := r.RecordUsageCount(context.Background(), "dup", "", 0); err == nil {
		t.Fatalf("count=0은 에러")
	}
}
