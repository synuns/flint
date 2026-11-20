package db

import (
	"context"
	"testing"
)

// TestRecordUsageCountDupCorrectionKind — R50-A: dup_correction kind가
// 화이트리스트에 등록돼 per-source 카운터가 실DB에서 기록된다
// (RecordDups의 "dup_"+source 파생 경로).
func TestRecordUsageCountDupCorrectionKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.RecordUsageCount(ctx, "dup_correction", "correction", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_correction): %v", err)
	}
}
