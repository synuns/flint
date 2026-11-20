// usage_v0190_test.go — read_events recording·retention cleanup slow
// tests (real DB).
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordReadEventAndPrune — RecordReadEvent records the numbers·
// flags·labels as-is, and PruneReadEvents deletes only rows past the
// 14-day retention (recent rows kept).
func slowRecordReadEventAndPrune(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6: %v", err)
	}
	// Clean this test's label rows (shared-DB integrity — in case of
	// prior-failure residue).
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
	// Roll the first row back 20 days, past the retention horizon
	// (verifying the cutoff parameter).
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

// TestPruneRetentionOverride — retention-day meta override — with
// events_retention_days generously raised, a past row that the default
// 14 days would delete survives.
func slowPruneRetentionOverride(t *testing.T, r *Repo) {
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
	// Override 100 days — the 20-day row must survive.
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
