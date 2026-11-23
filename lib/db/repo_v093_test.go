package db

import (
	"context"
	"testing"
)

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

	exists, err := r.ExistsHash(ctx, "v093-dup-1")
	if err != nil || !exists {
		t.Fatalf("ExistsHash 시드 해시 = (%v, %v), want (true, nil)", exists, err)
	}
}
