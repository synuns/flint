package db

import (
	"context"
	"testing"
	"time"
)

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
