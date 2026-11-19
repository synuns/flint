// prune_recall_v1910_test.go — R42-C·D regressions (slow real-DB —
// newTestRepo isolates a scratch DB per test, synthetic demo data
// only):
// ① PruneInvalidated orphan cleanup — deleting knowledge also removes
// residual recall_events·knowledge_reads rows, and after the audit·
// metering commits dolt_status shows no unstaged residue for the three
// tables. ② PruneRecallEvents retention — deletes only expired events
// (retained events kept)·the rerun is idempotent (0 rows on the 2nd).
package db

import (
	"context"
	"testing"
	"time"
)

// TestPruneInvalidatedCleansRecallOrphans — R42-C: invalidation pruning
// leaves no recall-history orphans (blocking multiplicative growth).
// Non-orphan retention targets (an active knowledge's events·history)
// are unchanged.
func slowPruneInvalidatedCleansRecallOrphans(t *testing.T, r *Repo) {
	ctx := context.Background()
	// Synthetic seed — 3 knowledge rows: 2 invalidated+expired (deletion
	// targets), 1 active.
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("r42c-old-a"), knowledgeRec("r42c-old-b"), knowledgeRec("r42c-keep"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	old1, old2, keep := ids[0], ids[1], ids[2]
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'invalidated', invalid_at = now() - interval '100 days'
		 WHERE id = ANY($1)`, []int{old1, old2}); err != nil {
		t.Fatalf("무효화 시드: %v", err)
	}
	// Recall events·per-item history — on both deletion targets and the
	// retained row.
	for _, id := range []int{old1, old2, keep} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO recall_events (knowledge_id, recalled_at)
			 SELECT $1::int, now() - interval '5 days' FROM generate_series(1, 3)`, id); err != nil {
			t.Fatalf("이벤트 시드 id=%d: %v", id, err)
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO knowledge_reads (knowledge_id, session, kind) VALUES ($1, 'r42c', 'read')`, id); err != nil {
			t.Fatalf("이력 시드 id=%d: %v", id, err)
		}
	}
	n, err := r.PruneInvalidated(ctx, time.Now().UTC().AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("PruneInvalidated: %v", err)
	}
	if n != 2 {
		t.Fatalf("삭제 지식 행 = %d, want 2", n)
	}
	// Orphan assertions — the deleted ids' events·history leave 0
	// residue; the retained row keeps 3·1.
	var orphanEv, keepEv int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = ANY($1)`, []int{old1, old2}).
		Scan(&orphanEv); err != nil {
		t.Fatalf("고아 이벤트 조회: %v", err)
	}
	if orphanEv != 0 {
		t.Errorf("recall_events 고아 %d행 잔존 — PruneInvalidated가 이력을 정리하지 않음(R42-C)", orphanEv)
	}
	var orphanReads int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_reads WHERE knowledge_id = ANY($1)`, []int{old1, old2}).
		Scan(&orphanReads); err != nil {
		t.Fatalf("고아 이력 조회: %v", err)
	}
	if orphanReads != 0 {
		t.Errorf("knowledge_reads 고아 %d행 잔존(R42-C)", orphanReads)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, keep).Scan(&keepEv); err != nil {
		t.Fatalf("보존 이벤트 조회: %v", err)
	}
	if keepEv != 3 {
		t.Errorf("보존 지식 이벤트 = %d, want 3(active 행 불변)", keepEv)
	}
	// After the audit·metering commits, dolt_status — no unstaged residue
	// for the three tables (recall_events absorbed by audit staging,
	// knowledge_reads recovered by the metering commit).
	if _, err := r.Commit(ctx, "test: prune orphans (knowledge hygiene path)"); err != nil {
		t.Fatalf("감사 커밋: %v", err)
	}
	if err := r.CommitTelemetry(ctx, "test"); err != nil {
		t.Fatalf("계측 커밋: %v", err)
	}
	for _, tbl := range []string{"public.knowledge", "public.recall_events", "public.knowledge_reads"} {
		var left int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM dolt_status WHERE table_name = $1 AND staged = false`, tbl).
			Scan(&left); err != nil {
			t.Fatalf("dolt_status 조회 %s: %v", tbl, err)
		}
		if left != 0 {
			t.Errorf("dolt_status %s unstaged 잔여 %d행 — 커밋 흡수 누락", tbl, left)
		}
	}
}

// TestPruneRecallEventsRetentionOnlyExpired — R42-D: the
// retention-horizon prune deletes only expired events (retained events
// kept — the ranking source's invariant window)·the rerun is idempotent
// (0 rows on the 2nd·residual count unchanged).
func slowPruneRecallEventsRetentionOnlyExpired(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("r42d-exp"), knowledgeRec("r42d-fresh"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	expiredID, freshID := ids[0], ids[1]
	// 4 expired events (120 days ago)·2 retained events (5 days ago)·1
	// retention-boundary event (89 days ago — the cutoff now()-90d is
	// computed later than the INSERT's now(), so an exact-90-day seed
	// crosses the boundary; 89 days is the safety margin).
	for _, e := range []struct {
		id  int
		n   int
		day string
	}{
		{expiredID, 4, "120"},
		{freshID, 2, "5"},
		{freshID, 1, "89"},
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO recall_events (knowledge_id, recalled_at)
			 SELECT $1::int, now() - ($2 || ' days')::interval FROM generate_series(1, $3::int)`,
			e.id, e.day, e.n); err != nil {
			t.Fatalf("이벤트 시드: %v", err)
		}
	}
	before := time.Now().UTC().AddDate(0, 0, -90)
	n, err := r.PruneRecallEvents(ctx, before)
	if err != nil {
		t.Fatalf("PruneRecallEvents: %v", err)
	}
	if n != 4 {
		t.Fatalf("삭제 이벤트 = %d, want 4(만료분만)", n)
	}
	var leftExp, leftFresh int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, expiredID).Scan(&leftExp); err != nil {
		t.Fatalf("만료 행 잔존 조회: %v", err)
	}
	if leftExp != 0 {
		t.Errorf("만료 지식 이벤트 %d행 잔존, want 0", leftExp)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, freshID).Scan(&leftFresh); err != nil {
		t.Fatalf("보존 행 잔존 조회: %v", err)
	}
	if leftFresh != 3 {
		t.Errorf("보존 지식 이벤트 = %d, want 3(5일 2건+89일 경계 1건)", leftFresh)
	}
	// Idempotent — the 2nd run yields 0 rows·unchanged residue.
	n2, err := r.PruneRecallEvents(ctx, before)
	if err != nil {
		t.Fatalf("PruneRecallEvents 재실행: %v", err)
	}
	if n2 != 0 {
		t.Errorf("재실행 삭제 = %d, want 0(멱등)", n2)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&leftFresh); err != nil {
		t.Fatalf("전체 잔존 조회: %v", err)
	}
	if leftFresh != 3 {
		t.Errorf("재실행 후 전체 이벤트 = %d, want 3(불변)", leftFresh)
	}
}
