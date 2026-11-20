package db

import (
	"context"
	"testing"
)

// TestRecordUsageCountDupCorrectionKind — R50-A: the dup_correction kind
// is registered in the whitelist so the per-source counter is recorded on
// the real DB (the "dup_"+source derivation path in RecordDups).
func TestRecordUsageCountDupCorrectionKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.RecordUsageCount(ctx, "dup_correction", "correction", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_correction): %v", err)
	}
}
