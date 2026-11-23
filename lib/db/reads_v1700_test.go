package db

import (
	"context"
	"testing"
	"time"
)

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

func slowBumpRecallScoresIncrements(t *testing.T, r *Repo) {
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

	if err := r.BumpRecallScores(ctx, nil); err != nil {
		t.Fatalf("BumpRecallScores(nil): %v", err)
	}
	c1b, _ := derivedRecall(t, r, ctx, ids[0])
	if c1b == nil || *c1b != 1 {
		t.Errorf("empty 호출 후 파생 cnt = %v, want 1(무변화)", c1b)
	}
}

func slowBumpRecallScoresDuplicateIDs(t *testing.T, r *Repo) {
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
