// admin_v0190_test.go — schema v6 (EnsureSchemaV6) slow tests (real
// DB). The read_events table promotion·idempotence·numeric-comparison
// gate regression.
package db

import (
	"context"
	"testing"
)

// TestV6PromoteIdempotent — on a DB that passed the v5 gates (the
// existing deployment path), EnsureSchemaV6 → version 6·the read_events
// table created (RecordReadEvent INSERT behavior proven). A second call
// is an idempotent no-op.
func slowV6PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	// Reproducing the existing deployment path: v1 → passing the v5 gates
	// in order.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4, r.EnsureSchemaV5,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV6: %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version = %q, want 6", v)
	}
	// Behavior proof of the table's existence — record·read back one
	// event row.
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 2, Session: "v6-probe"}); err != nil {
		t.Fatalf("RecordReadEvent on promoted table: %v", err)
	}
	var hits int
	var session string
	if err := r.pool.QueryRow(ctx,
		`SELECT hits, session FROM read_events WHERE session = $1`, "v6-probe").Scan(&hits, &session); err != nil {
		t.Fatalf("read_events probe row: %v", err)
	}
	if hits != 2 {
		t.Errorf("probe hits = %d, want 2", hits)
	}
	// Cleanup (shared-DB integrity — the event version of the
	// resetVersionToV1 convention).
	if _, err := r.pool.Exec(ctx, `DELETE FROM read_events WHERE session = $1`, "v6-probe"); err != nil {
		t.Fatalf("probe 정리: %v", err)
	}
	// The 2nd call is an idempotent no-op — version 6 kept.
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV6 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version after second call = %q, want 6", v)
	}
}

// TestV6GateNumeric — the gate compares numerically (>=). '10' looks
// lower under string comparison but is higher numerically, so it must
// no-op without recording the version or recreating tables (a
// downgrade-incident regression — the same contract as the V5 gate).
func slowV6GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10") // below '6' in string order, above 6 numerically.
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}
