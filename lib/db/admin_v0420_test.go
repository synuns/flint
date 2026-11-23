package db

import (
	"context"
	"strings"
	"testing"
)

var v9IndexNames = []string{
	"flint_read_events_ts_idx",
	"flint_hot_events_ts_idx",
	"flint_session_starts_ts_idx",
	"flint_knowledge_pending_idx",
}

func slowV9PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6, r.EnsureSchemaV7, r.EnsureSchemaV8,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v9-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v9 row: %v", err)
	}
	old := ids[0]
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1, Session: "v9-probe"}); err != nil {
		t.Fatalf("pre-v9 RecordReadEvent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, "v9-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM knowledge WHERE content_hash = $1`, "v9-old")
	})

	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV9: %v", err)
	}
	if v := readVersion(t, r); v != "9" {
		t.Fatalf("schema_version = %q, want 9", v)
	}

	for _, idx := range v9IndexNames {
		assertObject(t, r, ctx, idx)
	}

	assertPartialIndex(t, r, ctx, "flint_knowledge_pending_idx",
		[]string{"status = 'pending'", "invalid_at IS NULL"})

	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE id = $1`, old).Scan(&n); err != nil {
		t.Fatalf("기존 knowledge 행 조회: %v", err)
	}
	if n != 1 {
		t.Errorf("기존 행 유실: knowledge id=%d count = %d, want 1", old, n)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1`, "v9-probe").Scan(&n); err != nil {
		t.Fatalf("기존 read_events 행 조회: %v", err)
	}
	if n != 1 {
		t.Errorf("기존 행 유실: read_events count = %d, want 1", n)
	}

	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV9 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "9" {
		t.Fatalf("schema_version after second call = %q, want 9", v)
	}
}

func slowV9GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("EnsureSchemaV9(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func assertPartialIndex(t *testing.T, r *Repo, ctx context.Context, name string, conds []string) {
	t.Helper()
	var def string
	if err := r.pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`, name).Scan(&def); err != nil {
		t.Fatalf("인덱스 %s 정의 조회: %v", name, err)
	}
	parts := append([]string{"WHERE"}, conds...)
	for _, p := range parts {
		if !strings.Contains(def, p) {
			t.Errorf("인덱스 %s 정의에 %q 부재:\n got %q", name, p, def)
		}
	}
}
