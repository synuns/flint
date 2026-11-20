// admin_v08_test.go — schema v5 (EnsureSchemaV5) slow tests (real DB).
package db

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/sqlfs"
)

// setVersion — forces meta.schema_version for gate input. Setup·
// teardown restoration belongs to resetVersionToV1 (shared-DB
// integrity).
func setVersion(t *testing.T, r *Repo, v string) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ('schema_version', $1)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
		t.Fatalf("버전 %q 설정: %v", v, err)
	}
}

// readVersion — the current meta.schema_version.
func readVersion(t *testing.T, r *Repo) string {
	t.Helper()
	var v string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	return v
}

// TestV5PromoteIdempotent — on a DB that passed the v4 gates (the
// existing deployment path), EnsureSchemaV5 → version 5·the
// superseded_by column present (existing rows get the default null).
// A second call is an idempotent no-op.
func slowV5PromoteIdempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	// Reproducing the existing deployment path: v1 → passing the v4 gates
	// in order.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	// A pre-promotion existing row — superseded_by gets the default
	// null.
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
	// Updatable via the new column (a behavior proof of its presence —
	// the batch-invalidation point).
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET superseded_by = $2 WHERE id = $1`, old, old+1); err != nil {
		t.Fatalf("set superseded_by: %v", err)
	}
	// The 2nd call is an idempotent no-op — version 5 kept.
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV5 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version after second call = %q, want 5", v)
	}
}

// TestV5GateNumeric — the gate compares numerically (>=). '10' looks
// lower under string comparison ('10'<'5') but is higher numerically,
// so it must no-op. A regression preventing a recurrence of the
// downgrade incident (the string-equality check).
func slowV5GateNumeric(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10") // below '5' in string order, above 5 numerically.
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

// TestV4GateNoDowngradeFromV5 — running the existing v2~v4 gates on a
// v5 DB does not roll the version back (wiring is addition-only — the
// existing gate logic unchanged).
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

// TestFreshSchemaHasColumn — the fresh-install path — recreating
// knowledge with the real schema.sql includes superseded_by (default
// null), and the gate chain v2→v5 yields meta=5. The fake DDL lacks the
// v5 column, so this test alone pins the real sqlfs (verifying the
// deployed file regardless of the seam).
func slowFreshSchemaHasColumn(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	// Reproduce a fresh DB: drop the views·knowledge then the meta
	// version → Migrate recreates.
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
	// Sequential gate-chain promotion — a fresh DB rises naturally to v5
	// (the same order as the root.go wiring).
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
