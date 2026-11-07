// admin_v0200_test.go — v0.20.0 schema v7(EnsureSchemaV7) 슬로우 테스트(실DB).
// ids 컬럼 승격·session_starts·hot_events 생성·멱등·수치 비교 게이트 회귀.
package db

import (
	"context"
	"testing"
)

// TestV7PromoteIdempotent — Review Focus(test_v7_promote_idempotent): v6
// 게이트를 경유한 DB에서 EnsureSchemaV7 → 버전 7·read_events.ids 컬럼
// 존재(기록 동작 증명)·session_starts·hot_events 생성. 재호출 멱등.
func TestV7PromoteIdempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// 기존 배포 경로 재현: v1 → v6 게이트 순차 통과.
	for _, gate := range []func(context.Context) error{
		r.EnsureSchemaV2, r.EnsureSchemaV3, r.EnsureSchemaV4,
		r.EnsureSchemaV5, r.EnsureSchemaV6,
	} {
		if err := gate(ctx); err != nil {
			t.Fatalf("상위 게이트: %v", err)
		}
	}
	// v6 행 1건 — 승격 후 ids 기본값 ''로 읽힌다(additive 기본값).
	if err := r.RecordReadEvent(ctx, 1, false, false, false, "v7-probe", ""); err != nil {
		t.Fatalf("pre-v7 RecordReadEvent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM session_starts WHERE session = $1`, "v7-probe")
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM hot_events WHERE ids = $1`, "v7-probe")
	})

	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV7: %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version = %q, want 7", v)
	}
	// ids 컬럼 동작 증명 — 기존 행은 ''(기본), 신규 기록은 CSV 가독.
	var ids string
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("기존 행 ids 가독: %v", err)
	}
	if ids != "" {
		t.Errorf("승격 전 행 ids = %q, want 빈 문자열(기본값)", ids)
	}
	if err := r.RecordReadEvent(ctx, 2, false, false, false, "v7-probe", "7,9"); err != nil {
		t.Fatalf("post-v7 RecordReadEvent(ids): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1 AND hits = 2`, "v7-probe").Scan(&ids); err != nil {
		t.Fatalf("신규 행 ids 가독: %v", err)
	}
	if ids != "7,9" {
		t.Errorf("ids = %q, want 7,9", ids)
	}
	// 신규 테이블 동작 증명.
	if err := r.RecordSessionStart(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordSessionStart: %v", err)
	}
	if err := r.RecordHotEvent(ctx, "v7-probe"); err != nil {
		t.Fatalf("RecordHotEvent: %v", err)
	}
	// 2회째 호출은 멱등 no-op.
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV7 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "7" {
		t.Fatalf("schema_version after second call = %q, want 7", v)
	}
}

// TestV7GateNumeric — 게이트 수치 비교 회귀(V5·V6과 동일 계약).
func TestV7GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	setVersion(t, r, "10")
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 10(수치 비교 no-op)", v)
	}
}
