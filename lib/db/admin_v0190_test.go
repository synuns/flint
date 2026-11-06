// admin_v0190_test.go — v0.19.0 schema v6(EnsureSchemaV6) 슬로우 테스트(실DB).
// read_events 테이블 승격·멱등·수치 비교 게이트 회귀.
package db

import (
	"context"
	"testing"
)

// TestV6PromoteIdempotent — Review Focus(test_v6_promote_idempotent): v5
// 게이트를 경유한 DB(기존 배포 경로)에서 EnsureSchemaV6 → 버전 6·
// read_events 테이블 생성(RecordReadEvent INSERT 동작 증명). 재호출은
// 멱등 no-op.
func TestV6PromoteIdempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// 기존 배포 경로 재현: v1 → v5 게이트 순차 통과.
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
	// 테이블 존재의 동작 증명 — 이벤트 1행 기록·가독.
	if err := r.RecordReadEvent(ctx, 2, false, false, false, "v6-probe"); err != nil {
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
	// 정리(공유 DB 무결 — resetVersionToV1 관례의 이벤트 버전).
	if _, err := r.pool.Exec(ctx, `DELETE FROM read_events WHERE session = $1`, "v6-probe"); err != nil {
		t.Fatalf("probe 정리: %v", err)
	}
	// 2회째 호출은 멱등 no-op — 버전 6 유지.
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV6 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "6" {
		t.Fatalf("schema_version after second call = %q, want 6", v)
	}
}

// TestV6GateNumeric — Review Focus(test_v6_gate_numeric): 게이트는 수치
// 비교(>=)다. '10'은 문자열 비교로는 하위로 보이지만 수치로는 상위이므로
// 버전 기록·테이블 재생성 없이 no-op여야 한다(v0.6 다운그레이드 사고
// 회귀 — V5 게이트와 동일 계약).
func TestV6GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10") // 문자열 정렬로는 '6' 미만, 수치로는 6 초과.
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}
