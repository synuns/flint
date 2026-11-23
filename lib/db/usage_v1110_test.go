package db

import (
	"context"
	"testing"
	"time"
)

func TestRecordUsageCountDupCorrectionKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	if err := r.RecordUsageCount(ctx, "dup_correction", "correction", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_correction): %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := flatDailyValue(t, r, "dup_correction", today); got != 1 {
		t.Fatalf("daily:dup_correction:%s = %d, want 1 (meta flat-key proof)", today, got)
	}
}
