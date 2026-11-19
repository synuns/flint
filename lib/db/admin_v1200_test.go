// admin_v1200_test.go — schema v12 게이트(EnsureSchemaV12) slow 테스트
// (실DB). 계약: recall_events 테이블(knowledge_id·recalled_at + id_time
// 인덱스) 생성·recall_score수 합성 이벤트 백필(스코어>0 행만)·knowledge
// 2열(recall_score·last_recalled_at) 폐기(information_schema 실측)·버전
// 12 기록·재호출 멱등·신규 DB(schema.sql 경로)에 recall_events 존재.
package db

import (
	"context"
	"testing"
	"time"

	"github.com/synuns/flint/lib/sqlfs"
)

// backfillAt — 백필 합성 이벤트의 recalled_at 원천 검증용 고정 시각
// (COALESCE(last_recalled_at, now()) 중 last_recalled_at 경로).
var backfillAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// TestV12GateRecallAppendOnly — v11 DB(합성 지식 2건: recall_score 3·0)를
// v12로 승격: 스코어>0 행만 score수(3건) 합성 이벤트 적립·두 컬럼 폐기·
// 버전 12·재호출 멱등(이벤트 중복 없음).
func TestV12GateRecallAppendOnly(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// v11 상태 재현 — recall_events 제거·v11 컬럼 보증(IF NOT EXISTS)·버전 11.
	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS recall_events`); err != nil {
		t.Fatalf("v11 재현 recall_events 삭제: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS last_recalled_at timestamptz`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("v11 재현 컬럼 보증 %q: %v", stmt, err)
		}
	}
	setVersion(t, r, "11")

	// 합성 지식 2건 — hot(recall_score 3)·cold(0, 기본값 그대로).
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v12-hot"), knowledgeRec("v12-cold"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET recall_score = 3, last_recalled_at = $2 WHERE id = $1`,
		ids[0], backfillAt); err != nil {
		t.Fatalf("hot 지식 스코어 설정: %v", err)
	}

	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV12: %v", err)
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("schema_version = %q, want 12", v)
	}

	// ① recall_events 3건 — hot 지식만(score수 합성), cold는 0건.
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&n); err != nil {
		t.Fatalf("recall_events 카운트: %v", err)
	}
	if n != 3 {
		t.Fatalf("recall_events %d건, want 3(score수 합성 백필)", n)
	}
	var hotN, atN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1`, ids[0]).Scan(&hotN); err != nil {
		t.Fatalf("hot 지식 이벤트 카운트: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events WHERE knowledge_id = $1 AND recalled_at = $2`,
		ids[0], backfillAt).Scan(&atN); err != nil {
		t.Fatalf("백필 시각 검증: %v", err)
	}
	if hotN != 3 || atN != 3 {
		t.Fatalf("hot 지식 이벤트 = %d건(시각 정합 %d), want 3(3) — last_recalled_at 원천 백필", hotN, atN)
	}

	// ② knowledge 두 컬럼 폐기(information_schema 실측).
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("knowledge.%s 잔존 — v12 컬럼 폐기 계약 위반", col)
		}
	}

	// ③ idx_recall_events_id_time 인덱스 — statistics는 인덱스 컬럼마다
	// 1행(2컬럼 인덱스 → 2행)이므로 1 이상으로 존재를 단언한다.
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'recall_events' AND index_name = 'idx_recall_events_id_time'`).
		Scan(&idx); err != nil {
		t.Fatalf("recall_events 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("idx_recall_events_id_time 부재 — 인덱스 계약 위반")
	}

	// ④ 재호출 멱등 — no-op(버전 12 유지·이벤트 중복 없음·에러 없음).
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV12 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("재호출 후 schema_version = %q, want 12", v)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM recall_events`).Scan(&n); err != nil {
		t.Fatalf("재호출 후 recall_events 카운트: %v", err)
	}
	if n != 3 {
		t.Fatalf("재호출 후 recall_events %d건, want 3(멱등 — 중복 백필 없음)", n)
	}
}

// TestV12GateNumeric — 상위 버전 DB는 수치 비교로 no-op(다운그레이드 방지
// 회귀 — '12'<'9' 문자열 정렬 함정).
func TestV12GateNumeric(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// no-op 증명 기준선 — 컬럼 존재를 보증해 둔다(선행 테스트가 폐기했을 수
	// 있는 공유 DB 상태와 무관하게 성립시킨다).
	if _, err := r.pool.Exec(ctx,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("recall_score 보증: %v", err)
	}
	setVersion(t, r, "99") // 문자열 정렬로는 '12' 이전, 수치로는 상위.
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}
	// 상위 버전 DB의 knowledge 컬럼은 건드리지 않는다(no-op 증명).
	if !columnExists(t, r, "knowledge", "recall_score") {
		t.Fatalf("상위 버전 DB에서 knowledge.recall_score 폐기 — no-op 계약 위반")
	}
}

// TestV12FreshSchemaHasRecallEvents — 신규 설치 경로: 실 schema.sql 재생성 +
// 게이트 체인 v2→v12 순차 승격(root.go 배선과 동일) 후 recall_events·인덱스
// 존재·두 컬럼 폐기·버전 12.
func TestV12FreshSchemaHasRecallEvents(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events`,
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
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 12", v)
	}
	var kn int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'recall_events'`).
		Scan(&kn); err != nil {
		t.Fatal(err)
	}
	if kn != 1 {
		t.Fatalf("신규 경로 recall_events 부재")
	}
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'recall_events' AND index_name = 'idx_recall_events_id_time'`).
		Scan(&idx); err != nil {
		t.Fatalf("신규 경로 recall_events 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("신규 경로 idx_recall_events_id_time 부재")
	}
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 경로 knowledge.%s 잔존 — v12 승격 후 폐기 필요", col)
		}
	}
}

// TestV11GateFreshDirectToV12 — R43 직행 단락: 신규 DB(schema.sql v12
// 종착로 생성 — recall_events 존재·버전 3)는 v11 게이트에서 폐기 예정
// 2열(recall_score·last_recalled_at)을 add하지 않는다(구 동작은 v11이
// add하고 v12가 drop하는 DDL 왕복). 게이트 체인 v2~v11 실행 직후 실측:
// 2열 부재·recall_events 존재·버전 11 — v12로의 진짜 직행.
func TestV11GateFreshDirectToV12(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events`,
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
		if gate.Version > 11 {
			break // v11 직후 시점 실측 — v12는 아래 ②에서
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	// ① v11 게이트 직후 — 2열 부재(직행 단락 실측)·recall_events 존재·버전 11.
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 DB v11 게이트 직후 knowledge.%s add — add→drop 왕복(R43 직행 위반)", col)
		}
	}
	var hasEvents int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'recall_events'`).
		Scan(&hasEvents); err != nil {
		t.Fatal(err)
	}
	if hasEvents != 1 {
		t.Fatalf("신규 DB v11 게이트 후 recall_events 부재 — 직행 판별 원천 훼손")
	}
	if v := readVersion(t, r); v != "11" {
		t.Fatalf("v11 게이트 후 schema_version = %q, want 11", v)
	}
	// ② v12 게이트 — 왕복 없는 종착(버전 12·2열 부재 유지).
	for _, gate := range SchemaGates() {
		if gate.Version != 12 {
			continue
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 v12: %v", err)
		}
	}
	if v := readVersion(t, r); v != "12" {
		t.Fatalf("v12 게이트 후 schema_version = %q, want 12", v)
	}
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("신규 DB v12 게이트 후 knowledge.%s 잔존", col)
		}
	}
}
