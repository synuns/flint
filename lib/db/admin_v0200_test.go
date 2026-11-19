// admin_v0200_test.go — schema v7 (EnsureSchemaV7) slow tests (real
// DB). The ids-column promotion·session_starts·hot_events creation·
// idempotence·the numeric-comparison gate regression.
package db

import (
	"context"
	"testing"
)

// TestV7PromoteIdempotent — on a DB that passed the v6 gates,
// EnsureSchemaV7 → version 7·the read_events.ids column present
// (recording behavior proven)·session_starts·hot_events created. A
// second call is idempotent.
func TestV7PromoteIdempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// Reproducing the existing deployment path: v1 → passing the v6 gates
	// in order.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	// One v6 row — after promotion it reads with the ids default ''
	// (an additive default).
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1, Session: "v7-probe"}); err != nil {
		t.Fatalf("pre-v7 RecordReadEvent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM session_starts WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM hot_events WHERE ids = $1`, "v7-probe")
	})

	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV7: %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version = %q, want 7", v)
	}
	// ids-column behavior proof — the existing row is '' (default); new
	// records read back the CSV.
	var ids string
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("기존 행 ids 가독: %v", err)
	}
	if ids != "" {
		t.Errorf("승격 전 행 ids = %q, want 빈 문자열(기본값)", ids)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 2, Session: "v7-probe", IDs: "7,9"}); err != nil {
		t.Fatalf("post-v7 RecordReadEvent(ids): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1 AND hits = 2`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("신규 행 ids 가독: %v", err)
	}
	if ids != "7,9" {
		t.Errorf("ids = %q, want 7,9", ids)
	}
	// New-table behavior proof.
	if err := r.RecordSessionStart(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordSessionStart: %v", err)
	}
	if err := r.RecordHotEvent(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordHotEvent: %v", err)
	}
	// The 2nd call is an idempotent no-op.
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV7 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version after second call = %q, want 7", v)
	}
}

// TestV7GateNumeric — the gate numeric-comparison regression (the same
// contract as V5·V6).
func TestV7GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}
