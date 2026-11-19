// reads_v1200_test.go — the recording-wiring slow tests (real DB).
// Contract: RecordReadEvent's bytes·zero_reason·source 3-column values
// are recorded as-is in one read_events row, and RecordKnowledgeReads
// leaves the full id set in knowledge_reads together with session·kind
// (a kind-whitelist violation is an error).
package db

import (
	"context"
	"strconv"
	"testing"
)

// ensureV1200Schema — runs the gate chain (v2→v10) to idempotently
// secure the v10 outputs (3 columns·knowledge_reads), regardless of the
// shared DB's residual state.
func ensureV1200Schema(t *testing.T, r *Repo) {
	t.Helper()
	ctx := context.Background()
	resetVersionToV1(t, r)
	for _, gate := range SchemaGates() {
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 v%d: %v", gate.Version, err)
		}
	}
}

// TestRecordReadEventColumns — the ReadEvent extension 3 columns
// (bytes·zero_reason·source) store their values as-is.
func TestRecordReadEventColumns(t *testing.T) {
	r := newTestRepo(t)
	ensureV1200Schema(t, r)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(ctx, `DELETE FROM read_events WHERE session LIKE 't2-v1200%'`)
	})
	if err := r.RecordReadEvent(ctx, ReadEvent{
		Hits: 2, Session: "t2-v1200-a", IDs: "1,2",
		Bytes: 137, ZeroReason: "owned_miss", Source: "hook",
	}); err != nil {
		t.Fatalf("RecordReadEvent(확장 열): %v", err)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{
		Zero: true, Session: "t2-v1200-b", Bytes: 0, ZeroReason: "unowned", Source: "cli",
	}); err != nil {
		t.Fatalf("RecordReadEvent(제로히트): %v", err)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT session, bytes, zero_reason, source FROM read_events
		 WHERE session LIKE 't2-v1200%' ORDER BY session`)
	if err != nil {
		t.Fatalf("read_events 조회: %v", err)
	}
	defer rows.Close()
	var got [][4]string
	for rows.Next() {
		var sess, reason, src string
		var b int
		if err := rows.Scan(&sess, &b, &reason, &src); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [4]string{sess, strconv.Itoa(b), reason, src})
	}
	want := [][4]string{
		{"t2-v1200-a", "137", "owned_miss", "hook"},
		{"t2-v1200-b", "0", "unowned", "cli"},
	}
	if len(got) != len(want) {
		t.Fatalf("기록 행 수 = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("행 %d = %v, want %v(bytes·zero_reason·source 정합)", i, got[i], want[i])
		}
	}
}

// TestRecordKnowledgeReads — the full id set is recorded in
// knowledge_reads one row each, together with session·kind. Empty ids
// is a no-op; an invalid kind is an error.
func TestRecordKnowledgeReads(t *testing.T) {
	r := newTestRepo(t)
	ensureV1200Schema(t, r)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(ctx, `DELETE FROM knowledge_reads WHERE session = 't2-v1200'`)
	})
	if err := r.RecordKnowledgeReads(ctx, []int{11, 12}, "t2-v1200", "hot"); err != nil {
		t.Fatalf("RecordKnowledgeReads: %v", err)
	}
	if err := r.RecordKnowledgeReads(ctx, nil, "t2-v1200", "read"); err != nil {
		t.Fatalf("RecordKnowledgeReads(빈 ids): %v", err)
	}
	if err := r.RecordKnowledgeReads(ctx, []int{13}, "t2-v1200", "bogus"); err == nil {
		t.Fatal("무효 kind가 에러 없이 수용됨")
	}
	rows, err := r.pool.Query(ctx,
		`SELECT knowledge_id, kind FROM knowledge_reads
		 WHERE session = 't2-v1200' ORDER BY knowledge_id`)
	if err != nil {
		t.Fatalf("knowledge_reads 조회: %v", err)
	}
	defer rows.Close()
	var got [][2]any
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [2]any{id, kind})
	}
	if len(got) != 2 || got[0][0] != "11" || got[1][0] != "12" {
		t.Fatalf("knowledge_reads 행 = %v, want ids 11·12(전수)", got)
	}
	if got[0][1] != "hot" || got[1][1] != "hot" {
		t.Fatalf("kind = %v·%v, want hot·hot", got[0][1], got[1][1])
	}
}
