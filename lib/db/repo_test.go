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

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/store"
)

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

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	ctx := context.Background()
	if r := newScratchTestRepo(t, ctx); r != nil {
		return r
	}

	unlock := sharedDBLock(t)

	t.Cleanup(unlock)
	useFakeSQL(t)
	r, err := NewRepo(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, tbl := range []string{"knowledge", "sessions", "projects"} {
		if _, err := r.pool.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("delete %s: %v", tbl, err)
		}
	}
	return r
}

func newScratchTestRepo(t *testing.T, ctx context.Context) *Repo {
	t.Helper()
	r, err := NewRepo(ctx, testengine.Scratch(t))
	if err != nil {
		t.Fatalf("NewRepo(scratch): %v", err)
	}

	t.Cleanup(r.Close)
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(scratch): %v", err)
	}

	if _, err := r.pool.Exec(ctx, `SELECT dolt_add('-A')`); err != nil {
		t.Fatalf("scratch 베이스라인 dolt_add: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`SELECT dolt_commit('-m', $1)`, "test: scratch schema baseline"); err != nil {
		t.Fatalf("scratch 베이스라인 dolt_commit: %v", err)
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

func TestMigrateReappliesViews(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	plantStaleView(t, r.dsn)
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	rows, err := r.pool.Query(ctx, `SELECT * FROM active_knowledge LIMIT 0`)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}
	var cols []string
	for _, fd := range rows.FieldDescriptions() {
		cols = append(cols, string(fd.Name))
	}
	rows.Close()

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
		knowledgeRec("atomic-1"),
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

func overrideBatchStmtChunk(t *testing.T, n int) {
	t.Helper()
	saved := batchStmtChunk
	batchStmtChunk = n
	t.Cleanup(func() { batchStmtChunk = saved })
}

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

func TestInsertBatchBatchedAtomicAcrossChunks(t *testing.T) {
	r := newTestRepo(t)
	overrideBatchStmtChunk(t, 2)
	ctx := context.Background()
	recs := []map[string]any{
		knowledgeRec("across-1"),
		knowledgeRec("across-2"),
		knowledgeRec("across-1"),
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

var t6CommitHashRe = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

func t6LastCommitHash(t *testing.T, r *Repo) string {
	t.Helper()
	var h string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT commit_hash FROM dolt_log LIMIT 1`).Scan(&h); err != nil {
		t.Fatalf("dolt_log 최신 해시: %v", err)
	}
	return h
}

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

func t6ClearTelemetry(t *testing.T, r *Repo) {
	t.Helper()
	for _, tbl := range []string{"read_events", "hot_events", "knowledge_reads", "session_starts"} {
		if _, err := r.pool.Exec(context.Background(), "DELETE FROM "+tbl); err != nil {
			t.Fatalf("delete %s: %v", tbl, err)
		}
	}
}

func TestCommitExcludesTelemetryRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()

	if err := r.CommitTelemetry(ctx, "t6 baseline"); err != nil {
		t.Fatalf("베이스라인 CommitTelemetry: %v", err)
	}

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

func TestCommitTelemetrySeparateRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback) VALUES (1,false,false,false)`); err != nil {
		t.Fatalf("read_events 적립: %v", err)
	}

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

	if n := t6DiffRowCount(t, r, head, head2, "knowledge"); n != 0 {
		t.Fatalf("telemetry 커밋 diff knowledge = %d행, want 0(-A 스윕 폐지)", n)
	}
}

func TestReadAuditAttributionRealSlow(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)
	ctx := context.Background()

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
	prev := t6LastCommitHash(t, r)

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

	if n := t6DiffRowCount(t, r, prev, auditHead, "recall_events"); n < 1 {
		t.Fatalf("감사 커밋 diff recall_events = %d행, want ≥1(recall bump 귀속)", n)
	}
	if n := t6DiffRowCount(t, r, prev, auditHead, "knowledge"); n != 0 {
		t.Fatalf("감사 커밋 diff knowledge = %d행, want 0(회상 경로 무갱신 — append-only)", n)
	}
	if n := t6DiffRowCount(t, r, prev, auditHead, "meta"); n < 1 {
		t.Fatalf("감사 커밋 diff meta = %d행, want ≥1(증분 귀속)", n)
	}
	for _, tbl := range telemetryStageTables {
		if n := t6DiffRowCount(t, r, prev, auditHead, tbl); n != 0 {
			t.Fatalf("감사 커밋 diff %s = %d행, want 0(계측 미스테이징)", tbl, n)
		}
	}

	if n := t6DiffRowCount(t, r, auditHead, telHead, "read_events"); n < 1 {
		t.Fatalf("telemetry 커밋 diff read_events = %d행, want ≥1", n)
	}
	if n := t6DiffRowCount(t, r, auditHead, telHead, "knowledge_reads"); n < 1 {
		t.Fatalf("telemetry 커밋 diff knowledge_reads = %d행, want ≥1", n)
	}
	for _, tbl := range []string{"knowledge", "meta", "recall_events"} {
		if n := t6DiffRowCount(t, r, auditHead, telHead, tbl); n != 0 {
			t.Fatalf("telemetry 커밋 diff %s = %d행, want 0(-A 스윕 폐지)", tbl, n)
		}
	}
}

func TestCommitTelemetryNothingStagedQuiet(t *testing.T) {
	r := newTestRepo(t)
	t6ClearTelemetry(t, r)

	if err := r.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("1회차 CommitTelemetry: %v", err)
	}
	if err := r.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("스테이징 0행 재호출이 오류로 전파: %v(계측 — 조용해야 함)", err)
	}
}

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
