// reads_v1700_test.go — v1.7.0 T2 회상 강화 slow 테스트(실DB, schema v11).
// BumpRecallScores가 히트 행의 recall_score를 1 증분·last_recalled_at를
// 새기고 미지정 행은 무변화로 둔다. 빈 슬라이스 no-op·중복 id 무결은
// BumpReads 계약 승계.
package db

import (
	"context"
	"testing"
	"time"
)

// TestBumpRecallScoresIncrements — Review Focus(v1.7.0 T2): 히트 행만
// recall_score+1·last_recalled_at=now — 미지정 행은 0/NULL 유지(신규 항목
// 불이익 금지의 실측).
func TestBumpRecallScoresIncrements(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV11(ctx); err != nil {
		t.Fatalf("EnsureSchemaV11: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("t2-v1700-a"), knowledgeRec("t2-v1700-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.BumpRecallScores(ctx, []int{ids[0]}); err != nil {
		t.Fatalf("BumpRecallScores: %v", err)
	}
	var s1, s2 int
	var l1, l2 *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score, last_recalled_at FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&s1, &l1); err != nil {
		t.Fatalf("row1: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score, last_recalled_at FROM knowledge WHERE id = $1`, ids[1]).
		Scan(&s2, &l2); err != nil {
		t.Fatalf("row2: %v", err)
	}
	if s1 != 1 || l1 == nil {
		t.Errorf("히트 행 recall_score/last = %d/%v, want 1/non-null", s1, l1)
	}
	if s2 != 0 || l2 != nil {
		t.Errorf("미지정 행 recall_score/last = %d/%v, want 0/null(무변화)", s2, l2)
	}
	// 빈 슬라이스 no-op — 에러 없이 무변화.
	if err := r.BumpRecallScores(ctx, nil); err != nil {
		t.Fatalf("BumpRecallScores(nil): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score FROM knowledge WHERE id = $1`, ids[0]).Scan(&s1); err != nil {
		t.Fatalf("re-read row1: %v", err)
	}
	if s1 != 1 {
		t.Errorf("empty 호출 후 recall_score = %d, want 1(무변화)", s1)
	}
}

// TestBumpRecallScoresDuplicateIDs — id=ANY 중복 무결: 같은 id를 여러 번
// 전달해도 행은 1회만 증분된다(BumpReads 계약 승계).
func TestBumpRecallScoresDuplicateIDs(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV11(ctx); err != nil {
		t.Fatalf("EnsureSchemaV11: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("t2-v1700-dup")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.BumpRecallScores(ctx, []int{ids[0], ids[0], ids[0]}); err != nil {
		t.Fatalf("BumpRecallScores(dup): %v", err)
	}
	var s int
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score FROM knowledge WHERE id = $1`, ids[0]).Scan(&s); err != nil {
		t.Fatalf("row: %v", err)
	}
	if s != 1 {
		t.Errorf("중복 id 3회 전달 시 recall_score = %d, want 1(행 기준 1회)", s)
	}
}
