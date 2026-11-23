package db

import (
	"context"
	"testing"
	"time"
)

func slowBumpRecallScoresInsertsEvents(t *testing.T, r *Repo) {
	ctx := context.Background()

	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM recall_events`); err != nil {
		t.Fatalf("recall_events 초기화: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("t3-v1900-a"), knowledgeRec("t3-v1900-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	normT := func(t *time.Time) time.Time {
		if t == nil {
			return time.Time{}
		}
		return *t
	}
	type rowSnap struct {
		topic, summary string
		readCount      int
		lastRead       time.Time
		ingested       time.Time
	}
	snap := func(id int) rowSnap {
		var s rowSnap
		var lastRead, ingested *time.Time
		if err := r.pool.QueryRow(ctx,
			`SELECT topic, summary, read_count, last_read_at, ingested_at
			 FROM knowledge WHERE id = $1`, id).
			Scan(&s.topic, &s.summary, &s.readCount, &lastRead, &ingested); err != nil {
			t.Fatalf("스냅샷 id=%d: %v", id, err)
		}
		s.lastRead, s.ingested = normT(lastRead), normT(ingested)
		return s
	}
	beforeA, beforeB := snap(ids[0]), snap(ids[1])

	start := time.Now()
	if err := r.BumpRecallScores(ctx, []int{ids[0], ids[1]}); err != nil {
		t.Fatalf("BumpRecallScores: %v", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT knowledge_id, recalled_at FROM recall_events ORDER BY knowledge_id`)
	if err != nil {
		t.Fatalf("recall_events 조회: %v", err)
	}
	defer rows.Close()
	type ev struct {
		id int
		at time.Time
	}
	var events []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.at); err != nil {
			t.Fatalf("recall_events scan: %v", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recall_events rows: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("recall_events %d건, want 2(ids 각 1행)", len(events))
	}
	for _, e := range events {
		if e.id != ids[0] && e.id != ids[1] {
			t.Fatalf("예상 밖 지식 id 이벤트: %d", e.id)
		}

		if e.at.Before(start.Add(-time.Second)) {
			t.Fatalf("recalled_at %v < 적립 시각 %v — DEFAULT now() 미적용", e.at, start)
		}
	}

	afterA, afterB := snap(ids[0]), snap(ids[1])
	if afterA != beforeA {
		t.Errorf("히트 행 A 무변경 위반: %+v → %+v", beforeA, afterA)
	}
	if afterB != beforeB {
		t.Errorf("히트 행 B 무변경 위반: %+v → %+v", beforeB, afterB)
	}

	if err := r.BumpRecallScores(ctx, nil); err != nil {
		t.Fatalf("BumpRecallScores(nil): %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&n); err != nil {
		t.Fatalf("빈 ids 후 카운트: %v", err)
	}
	if n != 2 {
		t.Fatalf("빈 ids 호출 후 recall_events %d건, want 2(no-op)", n)
	}

	if err := r.BumpRecallScores(ctx, []int{ids[0], ids[0], ids[0]}); err != nil {
		t.Fatalf("BumpRecallScores(dup): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, ids[0]).
		Scan(&n); err != nil {
		t.Fatalf("중복 id 후 카운트: %v", err)
	}
	if n != 2 {
		t.Fatalf("중복 id 3회 전달 후 A 이벤트 %d건, want 2(행 기준 1회)", n)
	}
}

func slowRecallEventsAuditStaged(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM recall_events`); err != nil {
		t.Fatalf("recall_events 초기화: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("t3-v1900-stage")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	unstaged := func() int {
		var n int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM dolt_status
			 WHERE table_name = 'public.recall_events' AND staged = false`).Scan(&n); err != nil {
			t.Fatalf("dolt_status recall_events 조회: %v", err)
		}
		return n
	}
	if err := r.BumpRecallScores(ctx, ids); err != nil {
		t.Fatalf("BumpRecallScores: %v", err)
	}
	if n := unstaged(); n < 1 {
		t.Fatalf("적립 직후 recall_events unstaged 잔여 %d, want >=1(이벤트가 working set에 있음)", n)
	}
	if _, err := r.Commit(ctx, "read: recall bump (test staging)"); err != nil {
		t.Fatalf("감사 커밋: %v", err)
	}
	if n := unstaged(); n != 0 {
		t.Fatalf("감사 커밋 후 recall_events unstaged 잔여 %d, want 0(auditStageTables 흡수)", n)
	}
}

func slowReplayRecallEventsSynthesizesEvents(t *testing.T, r *Repo) {
	ctx := context.Background()

	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM recall_events`); err != nil {
		t.Fatalf("recall_events 초기화: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("t3-r42a-a"), knowledgeRec("t3-r42a-b"), knowledgeRec("t3-r42a-c"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	last := time.Date(2026, 11, 19, 9, 30, 0, 123456000, time.UTC)
	before := time.Now().UTC().Add(-time.Second)
	if err := r.ReplayRecallEvents(ctx, []RecallReplay{
		{ID: ids[0], Score: 3, LastRecalled: last},
		{ID: ids[1], Score: 2},
		{ID: ids[2], Score: 0},
	}); err != nil {
		t.Fatalf("ReplayRecallEvents: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)
	agg := func(id int) (int, time.Time) {
		var n int
		var mx *time.Time
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*), max(recalled_at) FROM recall_events WHERE knowledge_id = $1`, id).
			Scan(&n, &mx); err != nil {
			t.Fatalf("이벤트 집계 id=%d: %v", id, err)
		}
		if mx == nil {
			return n, time.Time{}
		}
		return n, *mx
	}
	if n, mx := agg(ids[0]); n != 3 || !mx.Equal(last) {
		t.Fatalf("지정 시각 재생: cnt=%d max=%v, want 3 %v", n, mx, last)
	}
	if n, mx := agg(ids[1]); n != 2 || mx.Before(before) || mx.After(after) {
		t.Fatalf("now 폴백 재생: cnt=%d max=%v, want 2 [%v, %v]", n, mx, before, after)
	}
	if n, _ := agg(ids[2]); n != 0 {
		t.Fatalf("score 0 재생: cnt=%d, want 0", n)
	}

	if err := r.ReplayRecallEvents(ctx, nil); err != nil {
		t.Fatalf("빈 슬라이스: %v", err)
	}
	if err := r.ReplayRecallEvents(ctx, []RecallReplay{{ID: ids[2]}}); err != nil {
		t.Fatalf("전부 score 0: %v", err)
	}
	if n, _ := agg(ids[2]); n != 0 {
		t.Fatalf("무효 재생 후 cnt=%d, want 0", n)
	}

	if _, err := r.Commit(ctx, "import: recall replay (test staging)"); err != nil {
		t.Fatalf("감사 커밋: %v", err)
	}
	var left int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM dolt_status
		 WHERE table_name = 'public.recall_events' AND staged = false`).Scan(&left); err != nil {
		t.Fatalf("dolt_status recall_events 조회: %v", err)
	}
	if left != 0 {
		t.Fatalf("감사 커밋 후 recall_events unstaged 잔여 %d, want 0", left)
	}
}
