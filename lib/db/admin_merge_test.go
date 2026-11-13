// admin_merge_test.go — R17 A2: Merge 계보 superseded_by 기록 slow 테스트.
// 병합 전이는 Supersede와 동일한 열·갱신 형식(superseded_by 단일 모델)을
// 따른다 — summary 마커만 남기던 계보 유실 결함 회귀.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestMergeRecordsSupersededBy — 병합 후 from행 superseded_by==into.
// (v1.2.3 T4 계약: Merge는 마커 suffix 관례와 함께 superseded_by를
// Supersede와 동일한 형식으로 기록한다.)
func TestMergeRecordsSupersededBy(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-sup-from"),
		knowledgeRec("merge-sup-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	if err := r.Merge(ctx, from, to); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var supersededBy *int
	var status string
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by, status FROM knowledge WHERE id = $1`, from).
		Scan(&supersededBy, &status); err != nil {
		t.Fatalf("read from: %v", err)
	}
	if supersededBy == nil || *supersededBy != to {
		t.Fatalf("from.superseded_by = %v, want %d", supersededBy, to)
	}
	if status != "invalidated" {
		t.Fatalf("from.status = %q, want invalidated", status)
	}
	// to는 그대로 active — 계보 수신자 무변화.
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&status); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if status != "active" {
		t.Fatalf("to.status = %q, want active", status)
	}
}

// TestMergeTwiceRejected — 이미 invalidated된 from 재병합 거부 + 마커 단일.
// 재병합은 Supersede의 0행 후견사 관례(ErrNotFound 랩 오류)로 거부한다.
func TestMergeTwiceRejected(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-twice-from"),
		knowledgeRec("merge-twice-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	if err := r.Merge(ctx, from, to); err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	if err := r.Merge(ctx, from, to); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on re-merge of invalidated from, got %v", err)
	}
	var summary string
	if err := r.pool.QueryRow(ctx,
		`SELECT summary FROM knowledge WHERE id = $1`, to).Scan(&summary); err != nil {
		t.Fatalf("read to: %v", err)
	}
	marker := fmt.Sprintf(" [merged:from=%d]", from)
	if n := strings.Count(summary, marker); n != 1 {
		t.Fatalf("marker count = %d, want 1 (summary %q)", n, summary)
	}
	// 계보도 첫 병합 값 그대로.
	var supersededBy *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, from).
		Scan(&supersededBy); err != nil {
		t.Fatalf("read from: %v", err)
	}
	if supersededBy == nil || *supersededBy != to {
		t.Fatalf("from.superseded_by = %v, want %d", supersededBy, to)
	}
}
