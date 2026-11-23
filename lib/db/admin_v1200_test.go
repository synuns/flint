package db

import (
	"context"
	"testing"
	"time"

	"github.com/synuns/flint/lib/sqlfs"
)

var backfillAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func slowV12GateRecallAppendOnly(t *testing.T, r *Repo) {
	ctx := context.Background()

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

	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			t.Fatalf("knowledge.%s 잔존 — v12 컬럼 폐기 계약 위반", col)
		}
	}

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

func slowV12GateNumeric(t *testing.T, r *Repo) {
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("recall_score 보증: %v", err)
	}
	setVersion(t, r, "99")
	if err := r.EnsureSchemaV12(ctx); err != nil {
		t.Fatalf("EnsureSchemaV12(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}

	if !columnExists(t, r, "knowledge", "recall_score") {
		t.Fatalf("상위 버전 DB에서 knowledge.recall_score 폐기 — no-op 계약 위반")
	}
}

func slowV12FreshSchemaHasRecallEvents(t *testing.T, r *Repo) {
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
		if gate.Version > 12 {

			break
		}
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

func slowV11GateFreshDirectToV12(t *testing.T, r *Repo) {
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
			break
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}

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
