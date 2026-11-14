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
// 재병합은 Supersede의 0행 후견사 관례(ErrNotFound 랩 오류)로 거부하며,
// 거부 문구는 이미 처리됨(invalidated) 케이스를 유지한다.
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
	} else if !strings.Contains(err.Error(), "이미 처리된") {
		t.Fatalf("re-merge of invalidated from: message %q, want \"이미 처리된\" 안내 유지",
			err.Error())
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

// TestMergePendingRejected — active 외 상태(pending) from 병합 거부.
// 0행 후견사는 상태를 구분해 안내한다: invalidated(이미 처리됨)가 아닌
// active 외 상태는 "active 아님(병합 불가) — status: <값>" 문구.
func TestMergePendingRejected(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-pending-from"),
		knowledgeRec("merge-pending-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	// from을 pending으로 강제 — active 갱신 조건 미충족 케이스 재현.
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'pending' WHERE id = $1`, from); err != nil {
		t.Fatalf("force pending: %v", err)
	}
	err = r.Merge(ctx, from, to)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on merge of pending from, got %v", err)
	}
	if !strings.Contains(err.Error(), "active") {
		t.Fatalf("merge of pending from: message %q, want \"active\" 안내", err.Error())
	}
	if !strings.Contains(err.Error(), "status: pending") {
		t.Fatalf("merge of pending from: message %q, want \"status: pending\" 구분 안내",
			err.Error())
	}
	// to는 무변화 — 거부 경로에서 마커 부착 없음.
	var toStatus string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&toStatus); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if toStatus != "active" {
		t.Fatalf("to.status = %q, want active", toStatus)
	}
}

// TestMergeActiveInvalidAtRejected — status='active' + invalid_at 설정
// (손상 상태) from도 0행 후견사로 거부된다. 이때 문구는 "active 아님 —
// status: active" 자기모순 대신 invalid_at 원인을 명시해야 한다(v1.2.4 T3).
func TestMergeActiveInvalidAtRejected(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-actinv-from"),
		knowledgeRec("merge-actinv-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	// 손상 상태 강제 — status는 active인데 invalid_at가 설정됨.
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET invalid_at = now() WHERE id = $1`, from); err != nil {
		t.Fatalf("force invalid_at: %v", err)
	}
	err = r.Merge(ctx, from, to)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on merge of active+invalid_at from, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_at") {
		t.Fatalf("active+invalid_at: message %q, want invalid_at 원인 명시", err.Error())
	}
	if strings.Contains(err.Error(), "active 아님") {
		t.Fatalf("active+invalid_at: message %q — status active에 자기모순 문구", err.Error())
	}
}
