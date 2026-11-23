package db

import (
	"context"
	"testing"
)

func slowV6PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

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

	if _, err := r.pool.Exec(ctx, `DELETE FROM read_events WHERE session = $1`, "v6-probe"); err != nil {
		t.Fatalf("probe 정리: %v", err)
	}

	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV6 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version after second call = %q, want 6", v)
	}
}

func slowV6GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}
