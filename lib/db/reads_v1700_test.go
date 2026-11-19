// reads_v1700_test.go — 회상 강화 slow 테스트(실DB, schema v12).
// v1.9.0 T4 이관: v11 knowledge.recall_score·last_recalled_at 직접 열
// SELECT 검증은 스키마 v12(열 폐기)에서 42703으로 파손돼, recall_events
// 파생 집계(랭킹 질의가 소비하는 같은 집계 조인)로 검증을 갱신한다.
// 이벤트 적립 원천 검증(reads_v1900_test.go)과 달리 여기선 파생 값
// cnt·last_at이 적립 결과를 정확히 반영하는지를 본다.
package db

import (
	"context"
	"testing"
	"time"
)

// derivedRecall — 랭킹 질의와 동일한 recall_events 파생 집계 조인으로
// 지식 1행의 (cnt, last_at)을 읽는다(미회상은 NULL, NULL).
func derivedRecall(t *testing.T, r *Repo, ctx context.Context, id int) (*int, *time.Time) {
	t.Helper()
	var cnt *int
	var last *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT re.cnt, re.last_at FROM knowledge k
		 LEFT JOIN (SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at
		            FROM recall_events GROUP BY knowledge_id) re
		 ON re.knowledge_id = k.id
		 WHERE k.id = $1`, id).Scan(&cnt, &last); err != nil {
		t.Fatalf("파생 집계 조회 id=%d: %v", id, err)
	}
	return cnt, last
}

// TestBumpRecallScoresIncrements — 히트 행의 회상 이벤트 적립이 파생
// cnt·last_at에 반영된다: 적립 행 cnt 1·last_at 최근, 미적립 행은
// NULL(미회상 — 랭킹 0점 원천). 빈 슬라이스 no-op.
func TestBumpRecallScoresIncrements(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM recall_events`); err != nil {
		t.Fatalf("recall_events 초기화: %v", err)
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
	c1, l1 := derivedRecall(t, r, ctx, ids[0])
	c2, l2 := derivedRecall(t, r, ctx, ids[1])
	if c1 == nil || *c1 != 1 || l1 == nil {
		t.Errorf("히트 행 파생 cnt/last = %v/%v, want 1/non-null", c1, l1)
	}
	if c2 != nil || l2 != nil {
		t.Errorf("미적립 행 파생 cnt/last = %v/%v, want nil/nil(미회상)", c2, l2)
	}
	// 빈 슬라이스 no-op — 에러 없이 파생 값 무변화.
	if err := r.BumpRecallScores(ctx, nil); err != nil {
		t.Fatalf("BumpRecallScores(nil): %v", err)
	}
	c1b, _ := derivedRecall(t, r, ctx, ids[0])
	if c1b == nil || *c1b != 1 {
		t.Errorf("empty 호출 후 파생 cnt = %v, want 1(무변화)", c1b)
	}
}

// TestBumpRecallScoresDuplicateIDs — 중복 id 무결: 같은 id를 여러 번
// 전달해도 행은 1회만 적립된다(파생 cnt 1 — BumpReads 계약 승계).
func TestBumpRecallScoresDuplicateIDs(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM recall_events`); err != nil {
		t.Fatalf("recall_events 초기화: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("t2-v1700-dup")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.BumpRecallScores(ctx, []int{ids[0], ids[0], ids[0]}); err != nil {
		t.Fatalf("BumpRecallScores(dup): %v", err)
	}
	c, _ := derivedRecall(t, r, ctx, ids[0])
	if c == nil || *c != 1 {
		t.Errorf("중복 id 3회 전달 시 파생 cnt = %v, want 1(행 기준 1회)", c)
	}
}
