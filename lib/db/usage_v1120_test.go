// usage_v1120_test.go — v1.12.0 T1: the dup_gap kind (the per-source
// dup counter of the `gap` metering channel) is registered in the
// RecordUsageCount whitelist so the "dup_"+source derivation path in
// RecordDups records it on the real DB.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountDupGapKind — one real-DB record round: the
// daily:dup_gap:<date> flat key holds the incremented count, and the
// unregistered spelling stays rejected (whitelist admission only).
func TestRecordUsageCountDupGapKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	if err := r.RecordUsageCount(ctx, "dup_gap", "gap", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_gap): %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := flatDailyValue(t, r, "dup_gap", today); got != 1 {
		t.Fatalf("daily:dup_gap:%s = %d, want 1 (meta flat-key proof)", today, got)
	}
	if err := r.RecordUsageCount(ctx, "dup_gaps", "", 1); err == nil {
		t.Fatal("RecordUsageCount(dup_gaps) 성공 — 미등록 kind 거부 없음")
	}
}
