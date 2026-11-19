// usage_v0341_test.go — "brief" added to the RecordUsageCount
// whitelist (additive). A regression test for the defect where
// lib/cli/brief.go's recordUsage(ctx, st, "brief", "") was rejected as
// an invalid kind and brief intake metering was never recorded.
// Real-DB slow — skipped in fast mode without FLINT_DSN.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountBrief — kind "brief" increments the daily counter
// and last_brief_at is recorded (not a dup-family kind, so it inherits
// the time-key contract). Invalid-kind rejection stays as before.
func TestRecordUsageCountBrief(t *testing.T) {
	r := newTestRepo(t)
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
	// Invalid-kind rejection is the existing contract as-is (the
	// whitelist-validation structure unchanged).
	if err := r.RecordUsageCount(context.Background(), "briefing", "", 1); err == nil {
		t.Fatalf("무효 kind briefing 수용 — 기존 거부 계약 위반")
	}
}
