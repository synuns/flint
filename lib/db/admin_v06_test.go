// admin_v06_test.go — schema v4 (EnsureSchemaV4) slow tests (real DB).
package db

import (
	"context"
	"testing"
	"time"
)

// TestEnsureSchemaV4Idempotent — 2 calls → the 2 columns·version 4·
// existing row values unchanged. Protocol: a DB that passed the v3
// gates (the existing deployment path) also promotes, and pre-promotion
// existing rows get the defaults read_count=0·last_read_at=null.
func TestEnsureSchemaV4Idempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// Reproducing the existing deployment path: a DB that passed v1 →
	// the v3 gates.
	if err := r.EnsureSchemaV3(ctx); err != nil {
		t.Fatalf("EnsureSchemaV3: %v", err)
	}
	// A pre-promotion existing row — the v4 columns must be filled with
	// defaults.
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v4-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v4 row: %v", err)
	}
	old := ids[0]
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV4: %v", err)
	}
	var readCount int
	var lastRead *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, old).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("read v4 columns of existing row: %v", err)
	}
	if readCount != 0 {
		t.Errorf("existing row read_count = %d, want 0 (기본값)", readCount)
	}
	if lastRead != nil {
		t.Errorf("existing row last_read_at = %v, want null", lastRead)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "4" {
		t.Fatalf("schema_version = %q, want 4", version)
	}
	// The 2nd call is an idempotent no-op — version 4·existing row values
	// unchanged.
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV4 (want idempotent): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("re-read schema_version: %v", err)
	}
	if version != "4" {
		t.Fatalf("schema_version after second call = %q, want 4", version)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, old).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("re-read v4 columns: %v", err)
	}
	if readCount != 0 || lastRead != nil {
		t.Errorf("기존 행 값 변형: read_count=%d last_read_at=%v, want 0/null", readCount, lastRead)
	}
}

// TestEnsureSchemaV4FromV1 — a v1 DB that skipped v3 also promotes with
// the single v4 gate (the gate only updates the version; the columns
// apply idempotently via IF NOT EXISTS).
func TestEnsureSchemaV4FromV1(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v4-from1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("EnsureSchemaV4 from v1: %v", err)
	}
	var readCount int
	var lastRead *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&readCount, &lastRead); err != nil {
		t.Fatalf("read v4 columns: %v", err)
	}
	if readCount != 0 || lastRead != nil {
		t.Errorf("v1 승격 행 read_count=%d last_read_at=%v, want 0/null", readCount, lastRead)
	}
	// Updatable via the new columns (a behavior proof of the columns'
	// presence).
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET read_count = read_count + 1, last_read_at = now()
		 WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("bump v4 columns: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count FROM knowledge WHERE id = $1`, ids[0]).Scan(&readCount); err != nil {
		t.Fatalf("re-read read_count: %v", err)
	}
	if readCount != 1 {
		t.Errorf("read_count after bump = %d, want 1", readCount)
	}
}

// TestEnsureSchemaGatesShortCircuitAbove — on a higher-version DB,
// lower gates skip even opening the transaction·commit (the
// numeric-comparison short circuit). When it was a string-equality
// check, EnsureSchemaV2 on a v3 DB opened the promotion transaction and
// rolled the version back to '2' (a downgrade record + widening the
// 40001 window under parallel startup — CI run 39717583172).
func TestEnsureSchemaGatesShortCircuitAbove(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '3')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("버전 '3' 설정: %v", err)
	}
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("EnsureSchemaV2(상위 버전 DB): %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if version != "3" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 3(상위 버전에서 no-op)", version)
	}
}
