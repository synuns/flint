package db

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/sqlfs"
)

func setVersion(t *testing.T, r *Repo, v string) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ('schema_version', $1)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
		t.Fatalf("버전 %q 설정: %v", v, err)
	}
}

func readVersion(t *testing.T, r *Repo) string {
	t.Helper()
	var v string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	return v
}

func slowV5PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v5-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v5 row: %v", err)
	}
	old := ids[0]
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV5: %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version = %q, want 5", v)
	}
	var sup *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, old).Scan(&sup); err != nil {
		t.Fatalf("read superseded_by of existing row: %v", err)
	}
	if sup != nil {
		t.Errorf("existing row superseded_by = %v, want null(기본값)", *sup)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET superseded_by = $2 WHERE id = $1`, old, old+1); err != nil {
		t.Fatalf("set superseded_by: %v", err)
	}

	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV5 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version after second call = %q, want 5", v)
	}
}

func slowV5GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

func slowV4GateNoDowngradeFromV5(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5: %v", err)
	}
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("하위 게이트(v5 DB): %v", err)
		}
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("하위 게이트가 버전 되돌림: schema_version=%q, want 5", v)
	}
}

func slowFreshSchemaHasColumn(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx,
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 삭제: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DROP TABLE knowledge`); err != nil {
		t.Fatalf("knowledge 삭제: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("버전 삭제: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	if v := readVersion(t, r); v != "1" {
		t.Fatalf("신규 생성 후 schema_version = %q, want 1", v)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v5-fresh")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	var sup *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, ids[0]).Scan(&sup); err != nil {
		t.Fatalf("신규 설치 경로 superseded_by 조회: %v", err)
	}
	if sup != nil {
		t.Errorf("신규 행 superseded_by = %v, want null", *sup)
	}

	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4, r.EnsureSchemaV5,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("게이트 체인: %v", err)
		}
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 5", v)
	}
}
