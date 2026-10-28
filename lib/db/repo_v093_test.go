// repo_v093_test.go — v0.9.3 회귀: DupIDByHash(해시 중복 시 기존 행 id
// 노출, R3). 시드 행 해시는 (id, true), 미등록 해시는 (0, false).
package db

import (
	"context"
	"testing"
)

// TestDupIDByHashSeededRow — 시드 행 해시 조회. slow · 실DB(newTestRepo —
// FLINT_DSN 미설정 시 스킵). ExistsHash 래퍼도 동일 판정을 유지한다.
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
	// 하위 호환 래퍼: ExistsHash는 dup 여부만 돌려준다.
	exists, err := r.ExistsHash(ctx, "v093-dup-1")
	if err != nil || !exists {
		t.Fatalf("ExistsHash 시드 해시 = (%v, %v), want (true, nil)", exists, err)
	}
}
