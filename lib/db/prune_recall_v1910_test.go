// prune_recall_v1910_test.go — R42-C·D 회귀(slow 실DB — newTestRepo
// 테스트당 scratch DB 격리, 합성 데모 데이터만):
// ① PruneInvalidated 고아 정리 — 지식 DELETE 시 recall_events·
// knowledge_reads 잔존 행이 같이 지워지고 감사·계측 커밋 후 dolt_status에
// 세 테이블 unstaged 잔여가 없다. ② PruneRecallEvents 보존기한 — 만료
// 이벤트만 삭제(보존 이벤트 유지)·재실행 멱등(2회째 0행).
package db

import (
	"context"
	"testing"
	"time"
)

// TestPruneInvalidatedCleansRecallOrphans — R42-C: 무효화 지식 정리가
// 회상 이력 고아를 남기지 않는다(성장 곱연산 차단). 고아가 아닌
// 보존 대상(active 지식의 이벤트·이력)은 불변.
func TestPruneInvalidatedCleansRecallOrphans(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// 합성 시드 — 지식 3행: 2행은 invalidated+만료(삭제 대상), 1행은 active.
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
	// 회상 이벤트·항목별 이력 — 삭제 대상 2행과 보존 행 모두에.
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
	// 고아 단언 — 삭제 대상 id의 이벤트·이력 잔존 0, 보존 행은 3·1 유지.
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
	// 감사·계측 커밋 후 dolt_status — 세 테이블 unstaged 잔여 없음
	// (recall_events는 감사 스테이징 흡수, knowledge_reads는 계측 커밋 회수).
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

// TestPruneRecallEventsRetentionOnlyExpired — R42-D: 보존기한 prune은
// 만료 이벤트만 삭제한다(보존 이벤트 유지 — 랭킹 원천 불변 구간)·
// 재실행은 멱등(2회째 0행·잔존 수 불변).
func TestPruneRecallEventsRetentionOnlyExpired(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("r42d-exp"), knowledgeRec("r42d-fresh"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	expiredID, freshID := ids[0], ids[1]
	// 만료 이벤트 4건(120일 전)·보존 이벤트 2건(5일 전)·보존 경계 이벤트
	// 1건(89일 전 — cutoff now()-90d는 INSERT now()보다 늦게 계산되므로
	// 90일 정각 seed는 경계 넘김, 89일로 안전 마진).
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
	// 멱등 — 2회째는 0행·잔존 불변.
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
