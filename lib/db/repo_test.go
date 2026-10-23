package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"

	"flint/lib/store"
)

// testDSN은 testutil_test.go로 이동 — FLINT_DSN 비빈 반환, 빈이면 스킵(5433 폴백 폐지).

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
SELECT id, topic, summary, tags, entity, domain
FROM knowledge WHERE invalid_at IS NULL;
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

// sharedDBLock은 공유 Doltgres DB를 쓰는 슬로우 테스트의 상호 배제(파일
// 잠금)다. go test ./...는 패키지 바이너리를 병렬로 띄우므로 잠금 없는
// 이 패키지의 DELETE·INSERT가 lib/backup의 dump·restore·drill 테스트(동일
// 잠금 사용)와 겹치면 Doltgres 낙관적 충돌(40001)·행수 어긋남이 간헐
// 실패한다(CI PR #1). 잠금 파일 경로는 lib/backup drillLock과 동일해야
// 상호 배제가 성립한다.
func sharedDBLock(t *testing.T) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// newTestRepo는 접속+마이그레이션 후 데이터를 비운 Repo를 돌려준다.
// 공유 DB 쓰기 전체(테스트 본문 포함)를 잠금으로 직렬화한다.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	unlock := sharedDBLock(t)
	// t.Cleanup은 LIFO — r.Close(아래 등록)가 먼저 실행되고 그다음 잠금 해제.
	t.Cleanup(unlock)
	useFakeSQL(t)
	ctx := context.Background()
	r, err := NewRepo(ctx, testDSN(t))
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

// TestMigrateSetsVersion — Migrate는 schema_version 부재(최초 생성) 시
// 스키마를 만들고 '1'을 기록한다. 공유 DB는 타 패키지(e2e·mcp)가 v3으로
// 올릴 수 있어 "방금 newTestRepo가 마이그레이션한 값=1" 기대는 상태
// 의존적이다(CI PR #1: want 1, got 3). meta 행을 지워 최초 생성 경로를
// 강제로 재현해 상태 무관하게 검증한다. 버전 상한은 Migrate 소관이
// 아니다(승격은 admin.go EnsureSchemaV2/V3 담당).
func TestMigrateSetsVersion(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("reset schema_version: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate after reset: %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "1" {
		t.Fatalf("want schema_version 1 after fresh Migrate, got %q", version)
	}
	// 재호급은 멱등이어야 한다.
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("re-read schema_version: %v", err)
	}
	if version != "1" {
		t.Fatalf("second Migrate changed version: %q, want 1", version)
	}
}

// TestMigratePreservesV3 — 회귀(CI PR #1): 공유 DB가 이미 v3일 때 Migrate는
// 버전을 내리거나 올리지 않는다(게이트는 n>=1 하한만 판정 — 상한·승격은
// EnsureSchemaV3 소관).
func TestMigratePreservesV3(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '3')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("plant v3: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`UPDATE meta SET value = '1' WHERE key = 'schema_version'`)
	})
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate under v3: %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "3" {
		t.Fatalf("schema_version = %q, want 3 preserved by Migrate", version)
	}
}

// 회귀(T15 인시던트): schema_version이 같아도 뷰 정의가 갱신되면
// Migrate가 최신 뷰로 재적용해야 한다 — 낡은 active_knowledge가 read를 깨뜨렸음.
func TestMigrateReappliesViews(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// 낡은(stale) 뷰 정의 심기 — simple protocol로 심어야 서버 카탈로그가
	// 실제로 1컬럼이 된다(확장 프로토콜 CREATE OR REPLACE는 컬럼 목록을
	// 갱신하지 않는 사고의 재현 조건, 인시던트 참조).
	plantStaleView(t, testDSN(t))
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
	// 최신 뷰 정의는 tags·entity·domain을 노출해야 한다 —
	// Doltgres CREATE OR REPLACE는 컬럼 목록을 갱신하지 않아(인시던트)
	// DROP+CREATE로 재적용해야 한다.
	want := []string{"id", "topic", "summary", "tags", "entity", "domain"}
	for _, w := range want {
		found := false
		for _, c := range cols {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("view columns after Migrate = %v, want %q included", cols, w)
		}
	}
}

// plantStaleView는 simple protocol 커넥션으로 active_knowledge를
// id 단일 컬럼 뷰로 교체한다.
func plantStaleView(t *testing.T, dsn string) {
	t.Helper()
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	conn, err := pgx.Connect(context.Background(),
		dsn+sep+"default_query_exec_mode=simple_protocol")
	if err != nil {
		t.Fatalf("simple connect: %v", err)
	}
	defer conn.Close(context.Background())
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("drop views: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`CREATE VIEW active_knowledge AS SELECT id FROM knowledge`); err != nil {
		t.Fatalf("plant stale view: %v", err)
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
