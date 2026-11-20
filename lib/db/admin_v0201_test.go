// admin_v0201_test.go — regressions for the status co-writing of
// invalidation·merge and promotion-zombie defense. Real-DB slow tests.
package db

import (
	"context"
	"testing"
)

// TestInvalidateSetsStatus — single invalidation·Merge(from) also write
// status='invalidated' — the same law as the batch path. The basis for
// read --status invalidated exposure·report count consistency.
func slowInvalidateSetsStatus(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("inv-status-a"), knowledgeRec("inv-status-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	a, b := ids[0], ids[1]

	if _, err := r.Invalidate(ctx, []int{a}, "패치 루프 회귀"); err != nil {
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

// TestApproveClearsInvalidAt — approving an invalidated pending clears
// invalid_at too — defense against the active+invalid_at zombie (a row
// visible neither in the view nor --status).
func slowApproveClearsInvalidAt(t *testing.T, r *Repo) {
	ctx := context.Background()
	rec := knowledgeRec("approve-zombie")
	rec["status"] = "pending"
	ids, err := r.InsertBatch(ctx, []map[string]any{rec})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id := ids[0]
	// Invalidate the pending row the same way as the batch law (a
	// pre-promotion invalidation scenario).
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
	// The promotion result must be visible in the active view
	// (invalid_at IS NULL).
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM active_knowledge WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("뷰 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("active 뷰 노출 %d건, want 1", n)
	}
}
