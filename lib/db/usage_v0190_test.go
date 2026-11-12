// usage_v0190_test.go — v0.19.0 read_events 기록·보존 정리 슬로우 테스트(실DB).
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordReadEventAndPrune — Review Focus(test_read_event_prune):
// RecordReadEvent는 수치·플래그·라벨을 그대로 기록하고, PruneReadEvents는
// 14일 보존을 넘긴 행만 삭제한다(최근 행 유지).
func TestRecordReadEventAndPrune(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6: %v", err)
	}
	// 이 테스트 라벨 행 정리(공유 DB 무결 — 이전 실패 잔여 대비).
	const label = "v0190-prune-probe"
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM read_events WHERE session = $1`, label); err != nil {
		t.Fatalf("사전 정리: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, label)
	})

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 3, Near: true, Fallback: true, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 0, Zero: true, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent(zero): %v", err)
	}
	// 첫 행을 20일 전으로 되돌려 보존 한도 밖으로(컷오프 파라미터 검증).
	if _, err := r.pool.Exec(ctx,
		`UPDATE read_events SET ts = $1
		  WHERE session = $2 AND hits = 3`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거 행 갱신: %v", err)
	}

	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1`, label).Scan(&n); err != nil {
		t.Fatalf("잔여 행 수 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("prune 후 잔여 %d행, want 1(최근 행만 유지)", n)
	}
	var hits int
	var zero, near, fallback bool
	if err := r.pool.QueryRow(ctx,
		`SELECT hits, zero, near, fallback FROM read_events WHERE session = $1`, label).
		Scan(&hits, &zero, &near, &fallback); err != nil {
		t.Fatalf("잔여 행 가독: %v", err)
	}
	if hits != 0 || !zero || near || fallback {
		t.Errorf("잔여 행 = {%d %v %v %v}, want {0 true false false}", hits, zero, near, fallback)
	}
}

// TestPruneRetentionOverride — v0.20.4: 보존일 meta 오버라이드 —
// events_retention_days를 넉넉히 하면 기본 14일로는 삭제될 과거 행이 생존.
func TestPruneRetentionOverride(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7: %v", err)
	}
	const label = "v0204-retention-probe"
	cleanup := func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM session_starts WHERE session = $1`, label)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := r.pool.Exec(ctx, `INSERT INTO session_starts (session) VALUES ($1)`, label); err != nil {
		t.Fatalf("세션 삽입: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE session_starts SET ts = $1 WHERE session = $2`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거화: %v", err)
	}
	// 오버라이드 100일 — 20일 행은 생존해야 한다.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('events_retention_days', '100')
		 ON CONFLICT (key) DO UPDATE SET value = '100'`); err != nil {
		t.Fatalf("오버라이드 설정: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM meta WHERE key = 'events_retention_days'`)
	})
	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM session_starts WHERE session = $1`, label).Scan(&n); err != nil {
		t.Fatalf("잔여 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("오버라이드 보존 실패 — 잔여 %d행, want 1(기본이었다면 0)", n)
	}
}
