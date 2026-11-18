// diff_store_test.go — DiffRows·DiffStat·DiffCommits(wire 히스토리
// 계약의 Doltgres 구현) slow 실DB 회귀. 합성 데모 지식 4커밋(추가→
// 추가→삭제→수정)을 심어 3종 diff_type·테이블별 요약·커밋 범위·순서를
// 실측한다. SQL은 lib/ops 원문 이식(바이트 동치)이라 커밋 구성도 원문
// 계약(added/removed는 COALESCE 공용 표현)을 따른다.
package db

import (
	"context"
	"testing"
)

// seedT5Commit — 커밋 1개 시드. sql이 비면 스테이징만(InsertBatch·
// pool.Exec 후 호출), 있으면 해당 SQL 실행 후 커밋. 커밋 해시 반환.
func seedT5Commit(t *testing.T, r *Repo, msg, sql string, args ...any) string {
	t.Helper()
	ctx := context.Background()
	if sql != "" {
		if _, err := r.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec(%s): %v", msg, err)
		}
	}
	h, err := r.Commit(ctx, msg)
	if err != nil {
		t.Fatalf("Commit(%s): %v", msg, err)
	}
	if h == "" {
		t.Fatalf("Commit(%s): 빈 해시(스테이징 0행)", msg)
	}
	return h
}

// TestDiffRowsSlowTypes — 커밋 쌍 3종에서 diff_type 실측. c1이
// newTestRepo의 미커밋 정리 DELETE를 흡수할 수 있어 added 검증은 c1
// 이후 구간(c1→c2)으로만 수행한다.
func TestDiffRowsSlowTypes(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v0180-t5-x"), // X — 수정 대상
	})
	if err != nil {
		t.Fatalf("InsertBatch(X): %v", err)
	}
	c1 := seedT5Commit(t, r, "flint v0180 t5 demo base-x", "")
	c2 := seedT5Commit(t, r, "flint v0180 t5 demo add-y",
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash)
		 VALUES ('토픽-v0180-t5-y', '요약', ':a:', 0.9, 'v0180-t5-y')`)
	c3 := seedT5Commit(t, r, "flint v0180 t5 demo rm-y",
		`DELETE FROM knowledge WHERE content_hash = 'v0180-t5-y'`)
	c4 := seedT5Commit(t, r, "flint v0180 t5 demo mod-x",
		`UPDATE knowledge SET topic = '토픽-v0180-t5-x2' WHERE id = $1`, ids[0])

	cases := []struct {
		name           string
		from, to       string
		wantType       string
		wantTopic      string
	}{
		{"added — c1→c2", c1, c2, "added", "토픽-v0180-t5-y"},
		{"removed — c2→c3", c2, c3, "removed", "토픽-v0180-t5-y"},
		{"modified — c3→c4", c3, c4, "modified", "토픽-v0180-t5-x2"},
	}
	for _, tc := range cases {
		rows, err := r.DiffRows(ctx, tc.from, tc.to)
		if err != nil {
			t.Fatalf("DiffRows(%s): %v", tc.name, err)
		}
		if len(rows) != 1 {
			t.Fatalf("DiffRows(%s) = %v, want 1행", tc.name, rows)
		}
		got := rows[0]
		if got.DiffType != tc.wantType || got.Topic != tc.wantTopic {
			t.Errorf("DiffRows(%s) = {%s %s}, want {%s %s}",
				tc.name, got.DiffType, got.Topic, tc.wantType, tc.wantTopic)
		}
		if got.ID == "" {
			t.Errorf("DiffRows(%s) ID 비어 있음: %+v", tc.name, got)
		}
	}
	// 동일 커밋 범위 — 차 없음(0행).
	same, err := r.DiffRows(ctx, c4, c4)
	if err != nil {
		t.Fatalf("DiffRows(동일 해시): %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("DiffRows(c4,c4) = %v, want 0행", same)
	}
}

// TestDiffStatSlowSummary — 커밋 해시 목록의 테이블별 변경 통계.
// 지식 커밋은 knowledge 테이블 data_change=true·schema_change=false.
func TestDiffStatSlowSummary(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t5-stat")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	h := seedT5Commit(t, r, "flint v0180 t5 demo stat", "")

	stats, err := r.DiffStat(ctx, []string{h})
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	found := false
	for _, s := range stats {
		if s.CommitHash != h {
			continue
		}
		found = true
		if s.Table == "knowledge" {
			if !s.DataChange {
				t.Errorf("DiffStat(knowledge).DataChange = false, want true")
			}
			if s.SchemaChange {
				t.Errorf("DiffStat(knowledge).SchemaChange = true, want false")
			}
		}
	}
	if !found {
		t.Fatalf("DiffStat(%s) = %v, want 해당 해시 행", h, stats)
	}
	// 빈 목록 — ANY($1) 빈 배열 오류 없이 0행.
	none, err := r.DiffStat(ctx, nil)
	if err != nil {
		t.Fatalf("DiffStat(nil): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("DiffStat(nil) = %v, want 0행", none)
	}
}

// TestDiffCommitsSlowRange — from(초과)~to(이하) 범위·최신순·data_change.
// LIMIT diffCommitsMax(20)은 원문 상수 승계 — 21커밋 시드 비용 대신
// 경계(동일 해시=빈 범위)·범위 밖 제외·DESC 순서로 계약 검증.
func TestDiffCommitsSlowRange(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t5-c0")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	c1 := seedT5Commit(t, r, "flint v0180 t5 demo r1", "")
	c2 := seedT5Commit(t, r, "flint v0180 t5 demo r2",
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash)
		 VALUES ('토픽-v0180-t5-c2', '요약', ':a:', 0.9, 'v0180-t5-c2')`)
	c3 := seedT5Commit(t, r, "flint v0180 t5 demo r3",
		`UPDATE knowledge SET summary = '요약2' WHERE content_hash = 'v0180-t5-c2'`)

	// c1(초과)~c3(이하) — c3, c2 DESC.
	cs, err := r.DiffCommits(ctx, c1, c3)
	if err != nil {
		t.Fatalf("DiffCommits: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("DiffCommits(c1,c3) = %d행, want 2행: %+v", len(cs), cs)
	}
	if cs[0].Hash != c3 || cs[1].Hash != c2 {
		t.Fatalf("DiffCommits 순서 = [%s %s], want [%s %s] DESC", cs[0].Hash, cs[1].Hash, c3, c2)
	}
	if cs[0].Message != "flint v0180 t5 demo r3" {
		t.Errorf("DiffCommits[0].Message = %q, want 커밋 메시지", cs[0].Message)
	}
	if cs[0].Date.IsZero() {
		t.Errorf("DiffCommits[0].Date 제로값: %+v", cs[0])
	}
	if !cs[0].DataChange {
		t.Errorf("DiffCommits[0].DataChange = false, want true(지식 수정 커밋)")
	}
	if cs[0].SchemaChange {
		t.Errorf("DiffCommits[0].SchemaChange = true, want false")
	}

	// 동일 해시 — 빈 범위(commit_order > h AND <= h).
	same, err := r.DiffCommits(ctx, c3, c3)
	if err != nil {
		t.Fatalf("DiffCommits(동일 해시): %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("DiffCommits(c3,c3) = %v, want 0행", same)
	}
}
