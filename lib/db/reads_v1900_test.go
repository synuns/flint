// reads_v1900_test.go — 회상 강화 INSERT 전환 slow 테스트(실DB,
// schema v12). BumpRecallScores가 UPDATE knowledge 대신 recall_events에
// 이벤트를 적립한다: read 히트 후 ① ids만큼 이벤트 적립 ② knowledge 행
// 데이터 무변경(UPDATE 부재 실측) ③ 빈 ids no-op ④ 중복 id 행 기준 1회.
package db

import (
	"context"
	"testing"
	"time"
)

// TestBumpRecallScoresInsertsEvents — 히트 ids의 회상 이벤트 적립·
// knowledge 행 무변경·빈 슬라이스 no-op·중복 id 1회(DISTINCT — 구 UPDATE
// id=ANY 계약 승계).
func TestBumpRecallScoresInsertsEvents(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// v12 상태 보증 — recall_events 존재·knowledge 2열 폐기(멱등).
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

	// 기준선 스냅샷 — UPDATE 부재 증명(값 비교. knowledge에는 수정 시각
	// 열이 없어 전 열 값을 비교한다. NULL 가능한 last_read_at은 zero값으로
	// 정규화해 포인터 주소가 아닌 값으로 비교한다).
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

	// ① recall_events 적립 — ids 각 1행, recalled_at은 적립 시각.
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
		// 허용 오차 1초 — now()는 서버 시각, start는 클라이언트 시각이라
		// 같은 머신에서도 수 ms 클록 drift가 난다(실측 2ms 오탐). 단언
		// 의미(적립 시점의 DEFAULT now() 적용 — 과거 시각 허용 안 함)는
		// 유지된다.
		if e.at.Before(start.Add(-time.Second)) {
			t.Fatalf("recalled_at %v < 적립 시각 %v — DEFAULT now() 미적용", e.at, start)
		}
	}

	// ② knowledge 행 데이터 무변경 — UPDATE 부재 실측.
	afterA, afterB := snap(ids[0]), snap(ids[1])
	if afterA != beforeA {
		t.Errorf("히트 행 A 무변경 위반: %+v → %+v", beforeA, afterA)
	}
	if afterB != beforeB {
		t.Errorf("히트 행 B 무변경 위반: %+v → %+v", beforeB, afterB)
	}

	// ③ 빈 ids no-op — 에러 없이 이벤트 무변화.
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

	// ④ 중복 id — 행 기준 1회(DISTINCT — 구 id=ANY 계약 승계).
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

// TestRecallEventsAuditStaged — 회상 이벤트는 감사 커밋에 흡수된다
// (v1.9.0 T3 룰링 — auditStageTables에 recall_events 포함): 적립 직후
// unstaged 잔여가 생기고, 감사 커밋("read: recall bump") 후 잔여 0 —
// 전역 -A 스윕 폐지 하 이 테이블을 스테이징하지 않으면 기기 간
// push/pull로 회상 이벤트가 전파되지 않는다(T6 전제).
func TestRecallEventsAuditStaged(t *testing.T) {
	r := newTestRepo(t)
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

// TestReplayRecallEventsSynthesizesEvents — 이식 스코어 재생 산식(R42-A):
// score수만큼 합성 이벤트 적립·recalled_at은 COALESCE(last, now())로
// EnsureSchemaV12 백필과 동일(지정 시각 승계·미회상 now 폴백)·score 0과
// 빈 슬라이스는 no-op. 감사 커밋 흡수(recall_events 스테이징)도 함께.
func TestReplayRecallEventsSynthesizesEvents(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// v12 상태 보증 — recall_events 존재·knowledge 2열 폐기(멱등).
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
		{ID: ids[0], Score: 3, LastRecalled: last}, // 지정 시각 승계
		{ID: ids[1], Score: 2},                     // 미회상 — now 폴백
		{ID: ids[2], Score: 0},                     // score 0 — 재생 생략
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
	// 빈 슬라이스·전부 무효(0) — no-op(오류·적립 없음).
	if err := r.ReplayRecallEvents(ctx, nil); err != nil {
		t.Fatalf("빈 슬라이스: %v", err)
	}
	if err := r.ReplayRecallEvents(ctx, []RecallReplay{{ID: ids[2]}}); err != nil {
		t.Fatalf("전부 score 0: %v", err)
	}
	if n, _ := agg(ids[2]); n != 0 {
		t.Fatalf("무효 재생 후 cnt=%d, want 0", n)
	}
	// 감사 커밋 흡수 — 재생 이벤트도 recall_events 스테이징에 흡수된다.
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
