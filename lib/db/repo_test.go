package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/synuns/flint/lib/store"
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

// 회귀(인시던트): schema_version이 같아도 뷰 정의가 갱신되면
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

// ── InsertBatch insertOne 루프 → pgx Batch 청크 전환 회귀. ──

// overrideBatchStmtChunk — 청크 문 수 축소(테스트 종료 시 원복).
func overrideBatchStmtChunk(t *testing.T, n int) {
	t.Helper()
	saved := batchStmtChunk
	batchStmtChunk = n
	t.Cleanup(func() { batchStmtChunk = saved })
}

// TestInsertBatchBatchedChunkedReturnsIDs — 청크를 넘는 배치도 RETURNING id
// 순서·행 수가 불변이다(청크 경계 2로 축소 — 5문 배치는 3왕복).
func TestInsertBatchBatchedChunkedReturnsIDs(t *testing.T) {
	r := newTestRepo(t)
	overrideBatchStmtChunk(t, 2)
	ctx := context.Background()
	recs := make([]map[string]any, 0, 5)
	for i := 0; i < 5; i++ {
		recs = append(recs, knowledgeRec(fmt.Sprintf("batched-%d", i)))
	}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("want 5 ids, got %d (%v)", len(ids), ids)
	}
	seen := map[int]bool{}
	for i, id := range ids {
		if id <= 0 {
			t.Fatalf("ids[%d] not positive: %v", i, ids)
		}
		if seen[id] {
			t.Fatalf("중복 id: %v", ids)
		}
		seen[id] = true
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 5 {
		t.Fatalf("want 5 rows, got %d", n)
	}
}

// TestInsertBatchBatchedAtomicAcrossChunks — 청크 경계를 넘어 오류(23505)
// 가 나도 배치 전체는 롤백된다(ErrDuplicate 랩·0행 — 기존 원자성 계약 승계).
func TestInsertBatchBatchedAtomicAcrossChunks(t *testing.T) {
	r := newTestRepo(t)
	overrideBatchStmtChunk(t, 2)
	ctx := context.Background()
	recs := []map[string]any{
		knowledgeRec("across-1"),
		knowledgeRec("across-2"),
		knowledgeRec("across-1"), // 3문째(청크 2 시작) — 첫 청크 커밋 없이 전체 폐기
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
		t.Fatalf("want 0 rows after rollback across chunks, got %d", n)
	}
}

// ── Doltgres 커밋 시점 unique 위반(동시 write 레이스) 분류. ──

// TestMapInsertErrConstraintRollback — 동시 write 레이스에서 Doltgres는
// unique 위반을 SQLSTATE 23505가 아닌 XX000 + "constraint violations,
// transaction rolled back" 메시지로 커밋 시점에 돌려준다(2026-11-17
// 실측). 이 분류가 빠지면 InsertBatch 오류가 write.go survive 경로(재확인·
// 재시도)에 도달하지 못해 레이스 패자의 고유줄이 유실된다.
func TestMapInsertErrConstraintRollback(t *testing.T) {
	cases := []struct {
		name string
		err  error
		dup  bool
	}{
		{"커밋 시점 레이스 위반(XX000)은 ErrDuplicate", &pgconn.PgError{
			Code: "XX000",
			Message: "Committing this transaction resulted in a working set with constraint violations, transaction rolled back. " +
				"Constraint violations: Type: Unique Key Constraint Violation, Name: knowledge_content_hash_key",
		}, true},
		{"동일 XX000이라도 제약 위반이 아닌 오류는 전파", &pgconn.PgError{
			Code: "XX000", Message: "nothing to commit, working set clean",
		}, false},
		{"23505(일반 unique 위반) 분류 불변", &pgconn.PgError{
			Code: "23505", Message: `duplicate key value violates unique constraint "knowledge_content_hash_key"`,
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapInsertErr(tc.err)
			if errors.Is(got, store.ErrDuplicate) != tc.dup {
				t.Fatalf("mapInsertErr 분류 = ErrDuplicate:%v, want %v (err=%v)",
					errors.Is(got, store.ErrDuplicate), tc.dup, got)
			}
		})
	}
}

// ── 계측 분리 커밋 — 감사 커밋 순수성·telemetry 별도 커밋. ──

// t6CommitHashRe — dolt_log 해시 문자셋(dolt_diff 인라인 전 재검증 —
// read.go asOfHashRe와 동일 규격, 소문자 base32계).
var t6CommitHashRe = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

// t6LastCommitHash — dolt_log 최신 커밋 해시(기본 정렬 = 최신 우선).
func t6LastCommitHash(t *testing.T, r *Repo) string {
	t.Helper()
	var h string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT commit_hash FROM dolt_log LIMIT 1`).Scan(&h); err != nil {
		t.Fatalf("dolt_log 최신 해시: %v", err)
	}
	return h
}

// t6DiffRowCount — dolt_diff(from,to,table) 변경 행 수. dolt_diff는
// 파라미터화된 인자를 받지 않는다(실측: "Invalid argument to dolt_diff") —
// 해시·테이블명 문자셋 재검증 후 리터럴 인라인.
func t6DiffRowCount(t *testing.T, r *Repo, from, to, table string) int {
	t.Helper()
	if !t6CommitHashRe.MatchString(from) || !t6CommitHashRe.MatchString(to) {
		t.Fatalf("dolt_diff 인라인 해시 문자셋 위반: %q→%q", from, to)
	}
	if !regexp.MustCompile(`^[a-z_][a-z0-9_]*$`).MatchString(table) {
		t.Fatalf("dolt_diff 인라인 테이블명 위반: %q", table)
	}
	var n int
	if err := r.pool.QueryRow(context.Background(),
		fmt.Sprintf("SELECT count(*) FROM dolt_diff('%s','%s','%s')", from, to, table)).Scan(&n); err != nil {
		t.Fatalf("dolt_diff(%s,%s,%s): %v", from, to, table, err)
	}
	return n
}

// t6ClearTelemetry — 계측 테이블 적립 잔여 삭제(newTestRepo는 knowledge·
// sessions·projects만 비운다 — 계측 테이블은 테스트가 직접 정리).
func t6ClearTelemetry(t *testing.T, r *Repo) {
	t.Helper()
	for _, tbl := range []string{"read_events", "hot_events", "knowledge_reads", "session_starts"} {
		if _, err := r.pool.Exec(context.Background(), "DELETE FROM "+tbl); err != nil {
			t.Fatalf("delete %s: %v", tbl, err)
		}
	}
}

// TestCommitExcludesTelemetryRealSlow — 감사 커밋(Commit)은 계측 테이블을 스테이징하지 않는다. read 1회 상당의
// read_events 적립 + write/done 상당의 knowledge·sessions 적립 후 Commit
// 하면 — 커밋 diff에 knowledge ≥1·sessions ≥1·read_events 0행(감사 커밋
// 순수성·세션 귀속 5테이블 룰링).
func TestCommitExcludesTelemetryRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()
	// 베이스라인 — fresh DB(커밋 이력에 계측 테이블이 아직 없는 환경:
	// Migrate DDL은 working set에만 있다)에서 dolt_diff(prev, head,
	// 'read_events')가 "table not found"로 실패하지 않게, 계측 테이블을
	// 먼저 커밋 이력에 올린 뒤 prev를 잡는(게이트 맹점 — fresh DB 검증).
	// 이미 초기화된 DB에서는 no-op 커밋이라 prev는 마지막 커밋 그대로.
	if err := r.CommitTelemetry(ctx, "t6 baseline"); err != nil {
		t.Fatalf("베이스라인 CommitTelemetry: %v", err)
	}
	// 감사 데이터(knowledge 1행 + sessions 1행) + 계측 데이터(read_events 1행).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback) VALUES (1,false,false,false)`); err != nil {
		t.Fatalf("read_events 적립: %v", err)
	}
	if _, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("t6-audit-1"),
		{"kind": "session", "agent": "claude", "summary": "세션"},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	prev := t6LastCommitHash(t, r)
	if _, err := r.Commit(ctx, "test: t6 감사 커밋 [session:0]"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	head := t6LastCommitHash(t, r)
	if head == prev {
		t.Fatal("감사 커밋이 생성되지 않음(스테이징 0행 의심)")
	}
	if n := t6DiffRowCount(t, r, prev, head, "read_events"); n != 0 {
		t.Fatalf("감사 커밋 diff read_events = %d행, want 0(계측 미스테이징)", n)
	}
	if n := t6DiffRowCount(t, r, prev, head, "knowledge"); n < 1 {
		t.Fatalf("감사 커밋 diff knowledge = %d행, want ≥1", n)
	}
	if n := t6DiffRowCount(t, r, prev, head, "sessions"); n < 1 {
		t.Fatalf("감사 커밋 diff sessions = %d행, want ≥1(done session-end 귀속)", n)
	}
}

// TestCommitTelemetrySeparateRealSlow — CommitTelemetry는 계측 테이블을 스테이징해 'telemetry: <cmd>' 커밋을
// 만든다 — 감사 커밋 다음 호출에서 read_events diff ≥1행. 전역 스윕
// (dolt_add('-A'))은 폐지 — 계측 시퀀스(read_events_id_seq 등 4종)는
// 명시 스테이징하고, 감사 테이블 시퀀스(knowledge_id_seq)는 telemetry
// 스테이징 대상 밖이라 working set에 남는다(실측 — 감사 변경 흡수
// 방지가 스윕 폐지 목적).
func TestCommitTelemetrySeparateRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback) VALUES (1,false,false,false)`); err != nil {
		t.Fatalf("read_events 적립: %v", err)
	}
	// 감사 커밋 1회 — knowledge_id_seq 잔여가 남는다(테이블 지정 스테이징).
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("t6-tel-1")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.Commit(ctx, "test: t6 감사 커밋(seq 잔여) [session:0]"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	head := t6LastCommitHash(t, r)
	if err := r.CommitTelemetry(ctx, "read"); err != nil {
		t.Fatalf("CommitTelemetry: %v", err)
	}
	head2 := t6LastCommitHash(t, r)
	if head2 == head {
		t.Fatal("telemetry 커밋이 생성되지 않음(스테이징 0행 의심)")
	}
	if n := t6DiffRowCount(t, r, head, head2, "read_events"); n < 1 {
		t.Fatalf("telemetry 커밋 diff read_events = %d행, want ≥1", n)
	}
	// v1.7.2 T1 명시 스테이징 실측 — 계측 시퀀스는 회수, 감사 시퀀스는
	// working set에 잔존(-A 스윕 폐지 — 감사 테이블 변경 흡수 방지).
	var telSeqLeft, auditSeqLeft int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM dolt_status WHERE table_name = 'public.read_events_id_seq' AND staged = false`).Scan(&telSeqLeft); err != nil {
		t.Fatalf("dolt_status 계측 seq 잔여 조회: %v", err)
	}
	if telSeqLeft != 0 {
		t.Fatalf("read_events_id_seq 잔여 %d건, want 0(명시 스테이징 회수)", telSeqLeft)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM dolt_status WHERE table_name = 'public.knowledge_id_seq' AND staged = false`).Scan(&auditSeqLeft); err != nil {
		t.Fatalf("dolt_status 감사 seq 잔여 조회: %v", err)
	}
	if auditSeqLeft != 1 {
		t.Fatalf("knowledge_id_seq 잔여 %d건, want 1(감사 seq — telemetry 스테이징 대상 밖)", auditSeqLeft)
	}
	// telemetry 커밋은 감사 테이블을 흡수하지 않는다(-A 스윕 폐지 핵심).
	if n := t6DiffRowCount(t, r, head, head2, "knowledge"); n != 0 {
		t.Fatalf("telemetry 커밋 diff knowledge = %d행, want 0(-A 스윕 폐지)", n)
	}
}

// TestReadAuditAttributionRealSlow — Review Focus(v1.7.2 T1·slow 실측,
// R38-C1): read 완료 경로의 감사 변경(recall bump UPDATE knowledge·meta
// 증분)은 감사 커밋(Commit)에, 계측 4종(read_events·hot_events·
// knowledge_reads·session_starts)은 telemetry 커밋(CommitTelemetry)에
// 귀속한다. 최신 2커밋의 dolt_diff로 실측 — -A 스윕이 감사 변경을 계측
// 커밋으로 흡수하던 C1 회귀 방어(read.go 완료 경로와 동일한 호출 순서).
func TestReadAuditAttributionRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()
	// 베이스라인 — 계측 테이블 삭제분을 커밋 이력에 올리고 감사 원본을
	// 확보한 뒤 prev를 잡는(fresh DB dolt_diff "table not found" 방어 —
	// TestCommitExcludesTelemetryRealSlow 관례 승계).
	if err := r.CommitTelemetry(ctx, "t1 baseline"); err != nil {
		t.Fatalf("베이스라인 CommitTelemetry: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("t1-read-audit")})
	if err != nil || len(ids) != 1 {
		t.Fatalf("InsertBatch: %v ids=%v", err, ids)
	}
	if _, err := r.Commit(ctx, "test: t1 감사 베이스라인 [session:0]"); err != nil {
		t.Fatalf("베이스라인 Commit: %v", err)
	}
	prev := t6LastCommitHash(t, r) // 감사 커밋 직전 해시(커밋 생성 전에 확보)
	// read 1회 상당의 적립 — 계측(read_events·knowledge_reads) + 감사
	// (recall bump UPDATE knowledge·meta 증분). hot_events·session_starts
	// 는 이 실행에 적립 없음(감사 커밋 순수성 단언 대상).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback) VALUES (1,false,false,false)`); err != nil {
		t.Fatalf("read_events 적립: %v", err)
	}
	if err := r.RecordKnowledgeReads(ctx, ids, "t1", "read"); err != nil {
		t.Fatalf("knowledge_reads 적립: %v", err)
	}
	if err := r.BumpRecallScores(ctx, ids); err != nil {
		t.Fatalf("recall bump: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('read_fallback_calls','1')
		 ON CONFLICT (key) DO UPDATE SET value = ((meta.value::bigint)+1)::text`); err != nil {
		t.Fatalf("meta 증분: %v", err)
	}
	// read 완료 경로 순서(lib/knowledge/read.go v1.7.2 T1) — 감사 커밋 후
	// 계측 커밋.
	if _, err := r.Commit(ctx, "read: recall bump (read audit attribution)"); err != nil {
		t.Fatalf("read 감사 커밋: %v", err)
	}
	auditHead := t6LastCommitHash(t, r)
	if auditHead == prev {
		t.Fatal("감사 커밋이 생성되지 않음(recall bump 스테이징 0행 의심)")
	}
	if err := r.CommitTelemetry(ctx, "read"); err != nil {
		t.Fatalf("CommitTelemetry: %v", err)
	}
	telHead := t6LastCommitHash(t, r)
	if telHead == auditHead {
		t.Fatal("telemetry 커밋이 생성되지 않음(스테이징 0행 의심)")
	}
	// 감사 커밋 diff — knowledge(recall bump)·meta ≥1, 계측 4종 0행.
	if n := t6DiffRowCount(t, r, prev, auditHead, "knowledge"); n < 1 {
		t.Fatalf("감사 커밋 diff knowledge = %d행, want ≥1(recall bump 귀속)", n)
	}
	if n := t6DiffRowCount(t, r, prev, auditHead, "meta"); n < 1 {
		t.Fatalf("감사 커밋 diff meta = %d행, want ≥1(증분 귀속)", n)
	}
	for _, tbl := range telemetryStageTables {
		if n := t6DiffRowCount(t, r, prev, auditHead, tbl); n != 0 {
			t.Fatalf("감사 커밋 diff %s = %d행, want 0(계측 미스테이징)", tbl, n)
		}
	}
	// telemetry 커밋 diff — 계측 ≥1, 감사(knowledge·meta) 0행.
	if n := t6DiffRowCount(t, r, auditHead, telHead, "read_events"); n < 1 {
		t.Fatalf("telemetry 커밋 diff read_events = %d행, want ≥1", n)
	}
	if n := t6DiffRowCount(t, r, auditHead, telHead, "knowledge_reads"); n < 1 {
		t.Fatalf("telemetry 커밋 diff knowledge_reads = %d행, want ≥1", n)
	}
	for _, tbl := range []string{"knowledge", "meta"} {
		if n := t6DiffRowCount(t, r, auditHead, telHead, tbl); n != 0 {
			t.Fatalf("telemetry 커밋 diff %s = %d행, want 0(-A 스윕 폐지)", tbl, n)
		}
	}
}

// TestCommitTelemetryNothingStagedQuiet — 계측 원칙(§13): 스테이징할 게
// 없으면(정상 working set) 오류가 아니다 — nothing-to-commit 관용.
func TestCommitTelemetryNothingStagedQuiet(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	// 커밋 후 working set에서 계측 잔여를 소진시킨 상태를 만든다.
	if err := r.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("1회차 CommitTelemetry: %v", err)
	}
	if err := r.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("스테이징 0행 재호출이 오류로 전파: %v(계측 — 조용해야 함)", err)
	}
}

// TestInsertBatchCommitRaceClassifiedRealSlow — 결정론적 교차로 실 Doltgres
// 커밋 시점 위반을 일으켜, 실제 오류 메시지가 mapInsertErr 분류에 잡히는지
// 검증한다(Dolt 메시지 드리프트 방어 — 실측 2026-11-17: XX000 +
// "constraint violations, transaction rolled back"). 교차: txA가 동일
// content_hash 삽입 후 보류 중 txB가 같은 해시를 삽입·커밋(승자)하면
// txA의 커밋이 working set 제약 위반으로 롤백된다.
func TestInsertBatchCommitRaceClassifiedRealSlow(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	const probeHash = "e1-race-probe-hash"
	ins := `INSERT INTO knowledge (topic, summary, tags, domain, confidence, origin, status, content_hash)
		VALUES ($1, '합성 프로브 줄', '', 'main', 0.9, 'agent', 'active', $2)`
	txA, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("txA Begin: %v", err)
	}
	defer txA.Rollback(ctx)
	if _, err := txA.Exec(ctx, ins, "e1-race-probe-a", probeHash); err != nil {
		t.Fatalf("txA 삽입: %v", err)
	}
	txB, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("txB Begin: %v", err)
	}
	if _, err := txB.Exec(ctx, ins, "e1-race-probe-b", probeHash); err != nil {
		t.Fatalf("txB 삽입: %v", err)
	}
	if err := txB.Commit(ctx); err != nil {
		t.Fatalf("txB 커밋(승자 — 성공해야 함): %v", err)
	}
	commitErr := txA.Commit(ctx)
	if commitErr == nil {
		t.Fatal("레이스 재현 실패 — txA 커밋이 성공함(교차가 성립하지 않음)")
	}
	if err := mapInsertErr(commitErr); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("실DB 커밋 시점 위반이 ErrDuplicate로 분류되지 않음(메시지 드리프트 의심): %v", err)
	}
}
