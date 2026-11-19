// usage_v0102_test.go — RecordUsageCount per-source dup keys:
// dup_hook·dup_manual·dup_mcp added to the whitelist (additive — the
// total "dup" unchanged). Invalid-kind rejection stays as before.
// Real-DB slow — skipped in fast mode without FLINT_DSN.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountDupSources — the 3 new kinds increment the daily
// counters under their own keys (ruling: whitelist addition only), and
// invalid kinds are still rejected.
func slowRecordUsageCountDupSources(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	for kind, n := range map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1} {
		if err := r.RecordUsageCount(context.Background(), kind, "", n); err != nil {
			t.Fatalf("RecordUsageCount(%s,%d): %v", kind, n, err)
		}
	}
	want := map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
		}
	}
	// Invalid-kind rejection is the existing contract as-is (the
	// whitelist-validation structure unchanged).
	if err := r.RecordUsageCount(context.Background(), "dup_webhook", "", 1); err == nil {
		t.Fatalf("무효 kind dup_webhook 수용 — 기존 거부 계약 위반")
	}
	// The dup family (total·source) records no last_ time key (inheriting
	// the total-dup contract — ruling: preventing accumulation of unused
	// meta keys like last_dup_hook_at).
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key LIKE 'last_dup%'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup%%: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup* 키 %d건 — dup 계열은 시각 키 없음", n)
	}
}
