// admin_v10_test.go — v1.2.0 T1 schema v10 게이트(EnsureSchemaV10) slow 테스트
// (실DB). 계약: read_events 3열(bytes·zero_reason·source)·knowledge_reads
// 테이블(knowledge_id·ts·session·kind + ts 인덱스) additive 추가·기존 데이터
// 무손상·멱등 승격·수치 비교 no-op(상위 버전 DB).
package db

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/sqlfs"
)

// columnExists — information_schema로 컬럼 존재 확인(게이트 additive 증명).
func columnExists(t *testing.T, r *Repo, table, column string) bool {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&n); err != nil {
		t.Fatalf("columns 조회 %s.%s: %v", table, column, err)
	}
	return n == 1
}

// TestV10GateAdditive — Review Focus: 기존 DB 무손상·멱등 승격·read_events 열
// 존재·knowledge_reads 인덱스. v9 DB(열·테이블 부재)를 재현해 승격 → 기존
// read_events 행은 기본값을 받고 knowledge 행은 무손상·재호출 멱등.
func TestV10GateAdditive(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// v9 DB 재현 — v10 산출물을 제거하고 버전을 9로 되돌린다. Doltgres는
	// DROP COLUMN IF EXISTS를 지원하지 않아 존재 확인 후 DROP한다.
	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS knowledge_reads`); err != nil {
		t.Fatalf("v9 재현 knowledge_reads 삭제: %v", err)
	}
	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if columnExists(t, r, "read_events", col) {
			if _, err := r.pool.Exec(ctx,
				`ALTER TABLE read_events DROP COLUMN `+col); err != nil {
				t.Fatalf("v9 재현 read_events.%s 삭제: %v", col, err)
			}
		}
	}
	setVersion(t, r, "9")

	// 승격 전 기존 데이터 — 무손상 증명의 기준선.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session)
		 VALUES (2, false, false, false, 'v10-pre')`); err != nil {
		t.Fatalf("seed pre-v10 read_event: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v10-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v10: %v", err)
	}

	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV10: %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("schema_version = %q, want 10", v)
	}

	// read_events 3열 — 기존 행은 기본값(0·''·'')을 받는다.
	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if !columnExists(t, r, "read_events", col) {
			t.Fatalf("read_events.%s 부재 — v10 additive 계약 위반", col)
		}
	}
	var bytes int
	var zeroReason, source string
	if err := r.pool.QueryRow(ctx,
		`SELECT bytes, zero_reason, source FROM read_events WHERE session = 'v10-pre'`).
		Scan(&bytes, &zeroReason, &source); err != nil {
		t.Fatalf("기존 read_event 행 조회: %v", err)
	}
	if bytes != 0 || zeroReason != "" || source != "" {
		t.Fatalf("기존 행 신규 열 = (%d,%q,%q), want (0,\"\",\"\",기본값)", bytes, zeroReason, source)
	}

	// knowledge_reads 테이블 + ts 인덱스 — 기록 동작 증명(T2가 배선).
	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'knowledge_reads'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("knowledge_reads 테이블 부재 — v10 계약 위반")
	}
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'knowledge_reads' AND index_name = 'flint_knowledge_reads_ts_idx'`).
		Scan(&idx); err != nil {
		t.Fatalf("knowledge_reads 인덱스 조회: %v", err)
	}
	if idx != 1 {
		t.Fatalf("flint_knowledge_reads_ts_idx 부재 — ts 인덱스 계약 위반")
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_reads (knowledge_id, session, kind) VALUES ($1, 'v10', 'read')`,
		ids[0]); err != nil {
		t.Fatalf("knowledge_reads 기록: %v", err)
	}

	// 기존 knowledge 무손상.
	var topic string
	if err := r.pool.QueryRow(ctx,
		`SELECT topic FROM knowledge WHERE id = $1`, ids[0]).Scan(&topic); err != nil {
		t.Fatalf("기존 knowledge 행 조회: %v", err)
	}
	if topic == "" {
		t.Fatalf("기존 knowledge 행 유실 — 무손상 계약 위반")
	}

	// 멱등 — 재호출은 no-op(버전 10 유지·에러 없음).
	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV10 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("재호출 후 schema_version = %q, want 10", v)
	}
}

// TestV10GateNumeric — 상위 버전 DB는 수치 비교로 no-op(다운그레이드 방지
// 회귀 — '10'<'9' 문자열 정렬 함정).
func TestV10GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	setVersion(t, r, "99") // 문자열 정렬로는 '10' 이전, 수치로는 상위.
	if err := r.EnsureSchemaV10(ctx); err != nil {
		t.Fatalf("EnsureSchemaV10(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}
}

// TestV10FreshSchemaHasArtifacts — 신규 설치 경로: 실 schema.sql 재생성 +
// 게이트 체인 v2→v10 순차 승격(root.go 배선과 동일) 후 3열·테이블 존재.
func TestV10FreshSchemaHasArtifacts(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("신규 DB 재현 %q: %v", stmt, err)
		}
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	for _, gate := range SchemaGates() {
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	if v := readVersion(t, r); v != "10" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 10", v)
	}
	for _, col := range []string{"bytes", "zero_reason", "source"} {
		if !columnExists(t, r, "read_events", col) {
			t.Fatalf("신규 경로 read_events.%s 부재", col)
		}
	}
	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'knowledge_reads'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("신규 경로 knowledge_reads 부재")
	}
}
