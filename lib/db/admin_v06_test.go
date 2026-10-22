// admin_v06_test.go — T59 schema v4(EnsureSchemaV4) slow 테스트(실DB).
package db

import (
	"context"
	"testing"
	"time"
)

// TestEnsureSchemaV4Idempotent — Review Focus(state): 2회 호출 → 컬럼 2종·
// 버전 4·기존 행 값 불변. protocol: v3 게이트를 경유한 DB(기존 배포 경로)도
// 승격되며, 승격 전 기존 행은 기본값 read_count=0·last_read_at=null을 받는다.
func TestEnsureSchemaV4Idempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// 기존 배포 경로 재현: v1 → v3 게이트 경유 DB.
	if err := r.EnsureSchemaV3(ctx); err != nil {
		t.Fatalf("EnsureSchemaV3: %v", err)
	}
	// 승격 전 기존 행 — v4 컬럼은 기본값으로 채워져야 한다.
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
	// 2회째 호출은 멱등 no-op — 버전 4·기존 행 값 무변화.
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

// TestEnsureSchemaV4FromV1 — v3을 건너뛴 v1 DB도 v4 게이트 하나로 승격된다
// (게이트는 버전만 갱신하고 컬럼은 IF NOT EXISTS로 멱등 적용).
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
	// 신규 컬럼으로 갱신 가능(컬럼 존재의 동작 증명).
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

// TestEnsureSchemaGatesShortCircuitAbove — 상위 버전 DB에서 하위 게이트는
// 트랜잭션·커밋 자체를 skip한다(수치 비교 단락). 문자열 동등 검사였을 때는
// v3 DB에서 EnsureSchemaV2가 승격 트랜잭션을 열어 버전을 '2'로 되돌렸다
// (하강 기록 + 병렬 기동의 40001 창 확대 — CI run 39717583172).
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
