package db

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"

	"flint/lib/store"
)

// testDSN은 slow 테스트용 DSN — 환경변수가 우선이다(pool_test.go와 동일 규칙).
func testDSN() string {
	if dsn := os.Getenv("FLINT_DSN"); dsn != "" {
		return dsn
	}
	return "postgresql://flint:flint-dev@localhost:5433/flint"
}

// fake DDL — 실 sqlfs(schema.sql)의 테스트에 필요한 하위집합.
// 컬럼 제약(UNIQUE content_hash 포함)은 실제와 동일해야 원자성 테스트가 유효하다.
const fakeSchemaSQL = `
CREATE TABLE IF NOT EXISTS projects (
    id      serial PRIMARY KEY,
    name    text NOT NULL UNIQUE,
    status  text NOT NULL DEFAULT 'active',
    updated timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
    id      serial PRIMARY KEY,
    ts      timestamptz NOT NULL DEFAULT now(),
    agent   text NOT NULL,
    summary text NOT NULL
);
CREATE TABLE IF NOT EXISTS knowledge (
    id           serial PRIMARY KEY,
    topic        text NOT NULL,
    summary      text NOT NULL,
    tags         text NOT NULL DEFAULT '',
    entity       text,
    domain       text NOT NULL DEFAULT 'main',
    confidence   double precision NOT NULL,
    origin       text NOT NULL DEFAULT 'agent',
    status       text NOT NULL DEFAULT 'active',
    content_hash text NOT NULL UNIQUE,
    session_id   integer REFERENCES sessions(id),
    valid_at     timestamptz NOT NULL DEFAULT now(),
    invalid_at   timestamptz,
    ingested_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);
`

const fakeViewsSQL = `
CREATE OR REPLACE VIEW active_knowledge AS
SELECT id, topic, summary FROM knowledge WHERE invalid_at IS NULL;
`

// useFakeSQL은 sqlfs seam을 테스트 DDL로 교체한다.
// SUPERLITE_REAL=1이면 실 sqlfs 구현을 그대로 쓴다(시험 seam 체크).
func useFakeSQL(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		return
	}
	savedSchema, savedViews := schemaSQLFn, viewsSQLFn
	schemaSQLFn = func() string { return fakeSchemaSQL }
	viewsSQLFn = func() string { return fakeViewsSQL }
	t.Cleanup(func() { schemaSQLFn, viewsSQLFn = savedSchema, savedViews })
}

// newTestRepo는 접속+마이그레이션 후 데이터를 비운 Repo를 돌려준다.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	useFakeSQL(t)
	ctx := context.Background()
	r, err := NewRepo(ctx, testDSN())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Doltgres는 TRUNCATE ... RESTART IDENTITY를 지원하지 않아 DELETE로 비운다.
	for _, tbl := range []string{"knowledge", "sessions", "projects"} {
		if _, err := r.pool.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("delete %s: %v", tbl, err)
		}
	}
	return r
}

func knowledgeRec(hash string) map[string]any {
	return map[string]any{
		"kind":         "knowledge",
		"topic":        "토픽-" + hash,
		"summary":      "요약",
		"tags":         ":a:",
		"confidence":   0.9,
		"content_hash": hash,
	}
}

func TestMigrateSetsVersion(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	var version string
	err := r.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "1" {
		t.Fatalf("want schema_version 1, got %q", version)
	}
	// 재호급은 멱등이어야 한다.
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

// 회귀(T15 인시던트): schema_version이 같아도 뷰 정의가 갱신되면
// Migrate가 최신 뷰로 재적용해야 한다 — 낡은 active_knowledge가 read를 깨뜨렸음.
func TestMigrateReappliesViews(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// 낡은(stale) 뷰 정의로 덮어쓰기 — 컬럼 축소 재현
	if _, err := r.pool.Exec(ctx,
		`CREATE OR REPLACE VIEW active_knowledge AS SELECT id FROM knowledge`); err != nil {
		t.Fatalf("plant stale view: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	// Doltgres는 information_schema에 뷰 컬럼을 노출하지 않으므로
	// 쿼리 필드 메타데이터로 검증한다.
	rows, err := r.pool.Query(ctx, `SELECT * FROM active_knowledge LIMIT 0`)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}
	var cols []string
	for _, fd := range rows.FieldDescriptions() {
		cols = append(cols, string(fd.Name))
	}
	rows.Close()
	// fake/real 공통: 뷰는 최소 id·topic·summary를 노출해야 한다(fake 3컬럼, real 전체).
	want := []string{"id", "topic", "summary"}
	if len(cols) < len(want) {
		t.Fatalf("view too narrow after Migrate: %v", cols)
	}
	for i, w := range want {
		if cols[i] != w {
			t.Fatalf("view columns after Migrate = %v, want prefix %v", cols, want)
		}
	}
}

func TestInsertBatchAtomic(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	recs := []map[string]any{
		knowledgeRec("atomic-1"),
		knowledgeRec("atomic-1"), // 동일 content_hash → unique 위반
	}
	_, err := r.InsertBatch(ctx, recs)
	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 rows after rollback, got %d", n)
	}
}

func TestInsertBatchReturnsIDs(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	recs := []map[string]any{
		knowledgeRec("ids-1"),
		{"kind": "session", "agent": "claude", "summary": "세션"},
	}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 ids, got %d (%v)", len(ids), ids)
	}
	for i, id := range ids {
		if id <= 0 {
			t.Fatalf("ids[%d] not positive: %v", i, ids)
		}
	}
}

func TestExistsHash(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("exists-1")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	got, err := r.ExistsHash(ctx, "exists-1")
	if err != nil {
		t.Fatalf("ExistsHash: %v", err)
	}
	if !got {
		t.Fatal("want true for stored hash")
	}
	got, err = r.ExistsHash(ctx, "missing")
	if err != nil {
		t.Fatalf("ExistsHash missing: %v", err)
	}
	if got {
		t.Fatal("want false for missing hash")
	}
}

func TestCommitReturnsHash(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("commit-1")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	hash, err := r.Commit(ctx, "test: T7 커밋 [session:0]")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-v]{20,}$`).MatchString(hash) {
		t.Fatalf("commit hash not hex-ish: %q", hash)
	}
}
