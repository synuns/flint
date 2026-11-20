// repo_v093_test.go — regression: DupIDByHash (exposing the existing
// row id on a hash duplicate, R3). A seeded row's hash yields (id,
// true); an unregistered hash yields (0, false).
package db

import (
	"context"
	"testing"
)

// TestDupIDByHashSeededRow — the seeded row's hash lookup. Slow · real
// DB (newTestRepo — skipped without FLINT_DSN). The ExistsHash wrapper
// keeps the same verdict.
func TestDupIDByHashSeededRow(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v093-dup-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id, dup, err := r.DupIDByHash(ctx, "v093-dup-1")
	if err != nil {
		t.Fatalf("DupIDByHash 시드 해시: %v", err)
	}
	if !dup || id != ids[0] {
		t.Fatalf("DupIDByHash 시드 해시 = (%d, %v), want (%d, true)", id, dup, ids[0])
	}
	id, dup, err = r.DupIDByHash(ctx, "v093-missing")
	if err != nil {
		t.Fatalf("DupIDByHash 미등록 해시: %v", err)
	}
	if dup || id != 0 {
		t.Fatalf("DupIDByHash 미등록 해시 = (%d, %v), want (0, false)", id, dup)
	}
	// The backward-compatible wrapper: ExistsHash returns dup presence
	// only.
	exists, err := r.ExistsHash(ctx, "v093-dup-1")
	if err != nil || !exists {
		t.Fatalf("ExistsHash 시드 해시 = (%v, %v), want (true, nil)", exists, err)
	}
}
