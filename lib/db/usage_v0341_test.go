package db

import (
	"context"
	"testing"
	"time"
)

func slowRecordUsageCountBrief(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	if err := r.RecordUsageCount(context.Background(), "brief", "", 2); err != nil {
		t.Fatalf("RecordUsageCount(brief,2): %v", err)
	}
	if got := flatDailyValue(t, r, "brief", today); got != 2 {
		t.Fatalf("daily:%s:brief = %d, want 2", today, got)
	}
	var last string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'last_brief_at'`).Scan(&last); err != nil {
		t.Fatalf("last_brief_at 부재 — brief는 시각 키 계약 대상: %v", err)
	}

	if err := r.RecordUsageCount(context.Background(), "briefing", "", 1); err == nil {
		t.Fatalf("무효 kind briefing 수용 — 기존 거부 계약 위반")
	}
}
