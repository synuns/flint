package db

import (
	"context"
	"testing"
	"time"
)

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

	if err := r.RecordUsageCount(context.Background(), "dup_webhook", "", 1); err == nil {
		t.Fatalf("무효 kind dup_webhook 수용 — 기존 거부 계약 위반")
	}

	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key LIKE 'last_dup%'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup%%: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup* 키 %d건 — dup 계열은 시각 키 없음", n)
	}
}
