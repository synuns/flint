// repo_v0180_test.go — regression: DupRowByHash (exposing the existing
// row's id·topic·status on a hash duplicate). A seeded row's hash
// matches its metadata; an unregistered hash yields (0,"",false).
package db

import (
	"context"
	"testing"
)

// TestDupRowByHashSeededRow — the seeded row's metadata lookup. Slow ·
// real DB (newTestRepo — skipped without FLINT_DSN). status is
// InsertBatch's default active.
func TestDupRowByHashSeededRow(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-dup-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id, topic, state, dup, err := r.DupRowByHash(ctx, "v0180-dup-1")
	if err != nil {
		t.Fatalf("DupRowByHash 시드 해시: %v", err)
	}
	if !dup || id != ids[0] {
		t.Fatalf("DupRowByHash 시드 해시 = (%d, %v), want (%d, true)", id, dup, ids[0])
	}
	if topic != "토픽-v0180-dup-1" {
		t.Fatalf("DupRowByHash topic = %q, want 시드 주제", topic)
	}
	if state != "active" {
		t.Fatalf("DupRowByHash state = %q, want active(InsertBatch 기본)", state)
	}
	id, topic, state, dup, err = r.DupRowByHash(ctx, "v0180-missing")
	if err != nil {
		t.Fatalf("DupRowByHash 미등록 해시: %v", err)
	}
	if dup || id != 0 || topic != "" || state != "" {
		t.Fatalf("DupRowByHash 미등록 해시 = (%d, %q, %q, %v), want 제로값·false", id, topic, state, dup)
	}
}
