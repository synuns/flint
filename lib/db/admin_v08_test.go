// admin_v08_test.go — T82 schema v5(EnsureSchemaV5) 슬로우 테스트(실DB).
package db

import (
	"context"
	"testing"

	"flint/lib/sqlfs"
)

// setVersion — 게이트 입력용으로 meta.schema_version을 강제한다.
// 시작·종료 복원은 resetVersionToV1이 담당한다(공유 DB 무결).
func setVersion(t *testing.T, r *Repo, v string) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ('schema_version', $1)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
		t.Fatalf("버전 %q 설정: %v", v, err)
	}
}

// readVersion — meta.schema_version 현재값.
func readVersion(t *testing.T, r *Repo) string {
	t.Helper()
	var v string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	return v
}

// TestV5PromoteIdempotent — Review Focus(test_v5_promote_idempotent):
// v4 게이트를 경유한 DB(기존 배포 경로)에서 EnsureSchemaV5 → 버전 5·
// superseded_by 컬럼 존재(기존 행은 기본 null). 재호출은 멱등 no-op.
func TestV5PromoteIdempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// 기존 배포 경로 재현: v1 → v4 게이트 순차 통과.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	// 승격 전 기존 행 — superseded_by는 기본 null을 받는다.
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
	// 신규 컬럼으로 갱신 가능(컬럼 존재의 동작 증명 — 배치 무효화 지점).
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET superseded_by = $2 WHERE id = $1`, old, old+1); err != nil {
		t.Fatalf("set superseded_by: %v", err)
	}
	// 2회째 호출은 멱등 no-op — 버전 5 유지.
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV5 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "5" {
		t.Fatalf("schema_version after second call = %q, want 5", v)
	}
}

// TestV5GateNumeric — Review Focus(test_v5_gate_numeric): 게이트는 수치
// 비교(>=)다. '10'은 문자열 비교('10'<'5')로는 하위로 보이지만 수치로는
// 상위이므로 no-op여야 한다. v0.6 다운그레이드 사고(문자열 동등 검사) 재발
// 방지 회귀.
func TestV5GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10") // 문자열 정렬로는 '5' 미만, 수치로는 5 초과.
	if err := r.EnsureSchemaV5(ctx); err != nil {
		t.Fatalf("EnsureSchemaV5(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}

// TestV4GateNoDowngradeFromV5 — Review Focus(기존 v4 게이트 회귀): v5 DB에서
// 기존 v2~v4 게이트를 돌려도 버전을 되돌리지 않는다(배선은 추가만 —
// 기존 게이트 논리 불변 확인).
func TestV4GateNoDowngradeFromV5(t *testing.T) {
	r := newTestRepo(t)
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

// TestFreshSchemaHasColumn — Review Focus(test_fresh_schema_has_column):
// 신규 설치 경로 — 실 schema.sql로 knowledge를 새로 만들면 superseded_by가
// 포함되고(기본 null), 게이트 체인 v2→v5 후 meta=5. fake DDL은 v5 컬럼이
// 없어 이 테스트만 실 sqlfs로 고정한다(seam과 무관하게 배포 파일 검증).
func TestFreshSchemaHasColumn(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	// 신규 DB 재현: 뷰·knowledge 제거 후 meta 버전 삭제 → Migrate 재생성.
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
	// 게이트 체인 순차 승격 — 신규 DB는 v5로 자연 상승(root.go 배선과 동일 순서).
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
