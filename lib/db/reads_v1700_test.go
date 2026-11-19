// reads_v1700_test.go — recall reinforcement slow tests (real DB,
// schema v12). v1.9.0 T4 transfer: verifying the v11
// knowledge.recall_score·last_recalled_at direct columns via SELECT
// breaks with 42703 under schema v12 (columns dropped), so the check
// was updated to the recall_events derived aggregate (the same aggregate
// join the ranking query consumes). Unlike the event-accrual source
// checks (reads_v1900_test.go), this file examines whether the derived
// values cnt·last_at accurately reflect the accrual result.
package db

import (
	"context"
	"testing"
	"time"
)

// derivedRecall — reads one knowledge row's (cnt, last_at) with the
// same recall_events derived aggregate join as the ranking query
// (never-recalled yields NULL, NULL).
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

// TestBumpRecallScoresIncrements — the recall-event accrual of hit rows
// is reflected in the derived cnt·last_at: the accrued row gets cnt 1·
// last_at recent; the unaccrued row gets NULL (never recalled — the
// ranking zero-score source). Empty slice no-op.
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
	// Empty slice no-op — no error, derived values unchanged.
	if err := r.BumpRecallScores(ctx, nil); err != nil {
		t.Fatalf("BumpRecallScores(nil): %v", err)
	}
	c1b, _ := derivedRecall(t, r, ctx, ids[0])
	if c1b == nil || *c1b != 1 {
		t.Errorf("empty 호출 후 파생 cnt = %v, want 1(무변화)", c1b)
	}
}

// TestBumpRecallScoresDuplicateIDs — duplicate-id immunity: passing the
// same id multiple times accrues the row once (derived cnt 1 —
// inheriting the BumpReads contract).
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
