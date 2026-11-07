// admin_v0201_test.go — v0.20.1(패치 루프 1차 F1): 무효화·병합의 status
// 병기와 승격 좀비 방어 회귀. 실DB 슬로우 테스트.
package db

import (
	"context"
	"testing"
)

// TestInvalidateSetsStatus — Review Focus(test_invalidate_status): 단일
// 무효화·Merge(from)이 status='invalidated'까지 병기 — 배치 경로와 단일
// 법칙. read --status invalidated 노출·report counts 정합의 근거.
func TestInvalidateSetsStatus(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("inv-status-a"), knowledgeRec("inv-status-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	a, b := ids[0], ids[1]

	if err := r.Invalidate(ctx, a, "패치 루프 회귀"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	var status string
	var invalidAt *string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, invalid_at::text FROM knowledge WHERE id = $1`, a).
		Scan(&status, &invalidAt); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "invalidated" || invalidAt == nil {
		t.Fatalf("Invalidate 후 = {%s %v}, want {invalidated <시각>} — 배치 경로와 단일 법칙", status, invalidAt)
	}

	if err := r.Merge(ctx, b, a); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, b).Scan(&status); err != nil {
		t.Fatalf("Merge from 조회: %v", err)
	}
	if status != "invalidated" {
		t.Fatalf("Merge(from) 후 status = %q, want invalidated", status)
	}
}

// TestApproveClearsInvalidAt — Review Focus(test_approve_zombie): 무효화된
// pending을 승인하면 invalid_at까지 청소 — active+invalid_at 좀비(뷰·
// --status 어디에도 안 보이는 행) 방어.
func TestApproveClearsInvalidAt(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	rec := knowledgeRec("approve-zombie")
	rec["status"] = "pending"
	ids, err := r.InsertBatch(ctx, []map[string]any{rec})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id := ids[0]
	// pending 행을 배치 법칙과 동일하게 무효화(승격 전 무효화 시나리오).
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'invalidated', invalid_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("사전 무효화: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'pending' WHERE id = $1`, id); err != nil {
		t.Fatalf("pending 복귀(무효화 표시 잔존 상태 재현): %v", err)
	}

	if err := r.Approve(ctx, id); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	var status string
	var invalidAt *string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, invalid_at::text FROM knowledge WHERE id = $1`, id).
		Scan(&status, &invalidAt); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "active" || invalidAt != nil {
		t.Fatalf("승인 후 = {%s %v}, want {active <nil>} — 잔존 invalid_at 좀비", status, invalidAt)
	}
	// 승격 결과는 active 뷰(invalid_at IS NULL)에 보여야 한다.
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM active_knowledge WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("뷰 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("active 뷰 노출 %d건, want 1", n)
	}
}
