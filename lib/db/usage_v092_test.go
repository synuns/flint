// usage_v092_test.go — RecordUsageCount: bulk dup N increments (real-DB
// slow). Contract: one count call adds N to the daily counter
// (replacing recordDups' O(N) read-writes); dup carries no last_ time
// key; count<1 is an error.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountBatches — RecordUsageCount("dup","",3)+
// ("write","hook",2) are bulk-reflected into daily with one upsert each
// (regression: an N-iteration RecordUsage loop).
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

	want := map[string]int{"dup": 3, "write": 2, "hook": 2}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
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
