// admin_v0420_test.go — v0.42.0 schema v9(EnsureSchemaV9) 슬로우 테스트(실DB).
// 계측 ts 인덱스 3종·knowledge pending partial 인덱스 승격·additive
// 무손상·멱등·수치 비교 게이트 회귀.
package db

import (
	"context"
	"strings"
	"testing"
)

// v9IndexNames — 게이트가 만드는 인덱스 4종(계측 테이블 ts 경로 3종 +
// knowledge pending partial 1종 — v0.42.0 파생 산출 쿼리 경로).
var v9IndexNames = []string{
	"flint_read_events_ts_idx",
	"flint_hot_events_ts_idx",
	"flint_session_starts_ts_idx",
	"flint_knowledge_pending_idx",
}

// TestV9PromoteIdempotent — Review Focus(test_v9_promote_idempotent): v8
// 게이트를 경유한 DB(기존 배포 경로)에서 EnsureSchemaV9 → 버전 9·
// 인덱스 4종 존재·승격 전 기존 행 무손상(additive). 재호출은 멱등 no-op.
func TestV9PromoteIdempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// 기존 배포 경로 재현: v1 → v8 게이트 순차 통과.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6, r.EnsureSchemaV7, r.EnsureSchemaV8,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	// 승격 전 기존 행 — additive 무손상 증명 입력.
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
	// 인덱스 4종 존재 단언.
	for _, idx := range v9IndexNames {
		assertObject(t, r, ctx, idx)
	}
	// pending partial 인덱스 — WHERE 절이 승격 약속 그대로인지 정의 단언
	// (일반 인덱스로 조용히 변해도 존재 단언만으론 못 잡는다).
	assertPartialIndex(t, r, ctx, "flint_knowledge_pending_idx",
		[]string{"status = 'pending'", "invalid_at IS NULL"})
	// 기존 행 무손상(additive — 게이트가 데이터를 건드리지 않는다).
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
	// 2회째 호출은 멱등 no-op.
	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV9 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "9" {
		t.Fatalf("schema_version after second call = %q, want 9", v)
	}
}

// TestV9GateNumeric — 게이트 수치 비교 회귀(V5~V7과 동일 계약):
// '10'은 수치로는 상위이므로 no-op여야 한다(다운그레이드 방지).
func TestV9GateNumeric(t *testing.T) {
	r := newTestRepo(t)
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

// assertPartialIndex — pg_indexes.indexdef에 WHERE 절과 조건 부문자열
// 전부가 포함되는지 단언(Doltgres는 조건을 테이블 한정
// "knowledge.status = 'pending'" 형태로 정규화하므로 낱개 부문자열 검사).
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
