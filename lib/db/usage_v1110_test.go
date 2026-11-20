package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountDupCorrectionKind — R50-A: the dup_correction kind
// is registered in the whitelist so the per-source counter is recorded on
// the real DB (the "dup_"+source derivation path in RecordDups).
// R52: the recorded value is also verified directly in meta — the
// daily:dup_correction:<date> flat key holds the incremented count.
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
