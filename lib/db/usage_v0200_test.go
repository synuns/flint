// usage_v0200_test.go — session boundaries·hot exposure recording·the
// 3-table unified retention cleanup slow tests (real DB).
package db

import (
	"context"
	"testing"
	"time"
)

// TestSessionStartAndHotEventRecords — the recording methods leave the
// values as-is, and the 14-day retention cleanup (PruneReadEvents)
// deletes only the expired portion across all 3 tables.
func slowSessionStartAndHotEventRecords(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7: %v", err)
	}
	const label = "v0200-prune-probe"
	cleanup := func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM session_starts WHERE session = $1`, label)
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM hot_events WHERE ids = $1`, label)
	}
	cleanup()
	t.Cleanup(cleanup)

	if err := r.RecordSessionStart(ctx, label); err != nil {
		t.Fatalf("RecordSessionStart: %v", err)
	}
	if err := r.RecordHotEvent(ctx, label); err != nil {
		t.Fatalf("RecordHotEvent: %v", err)
	}
	// Roll the 2 session starts back 20 days, past the retention
	// horizon.
	if _, err := r.pool.Exec(ctx,
		`UPDATE session_starts SET ts = $1 WHERE session = $2`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거 세션 갱신: %v", err)
	}

	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents(3테이블 통합): %v", err)
	}
	var sessN, hotN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM session_starts WHERE session = $1`, label).Scan(&sessN); err != nil {
		t.Fatalf("session_starts 잔여 조회: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM hot_events WHERE ids = $1`, label).Scan(&hotN); err != nil {
		t.Fatalf("hot_events 잔여 조회: %v", err)
	}
	if sessN != 0 {
		t.Errorf("20일 경과 세션 시작 잔여 %d행, want 0(정리)", sessN)
	}
	if hotN != 1 {
		t.Errorf("hot 노출 잔여 %d행, want 1(최근 행 유지)", hotN)
	}
}
