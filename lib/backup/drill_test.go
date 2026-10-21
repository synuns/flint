package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"flint/lib/db"
)

// Review Focus(numeric): INSERT 문 수와 테이블명 매칭 — knowledge만 세고
// 다른 테이블·주석·DELETE는 세지 않는다.
func TestCountInserts(t *testing.T) {
	stmts := splitStatements(`
-- flint backup probe
CREATE TABLE knowledge (id int);
BEGIN;
DELETE FROM knowledge;
INSERT INTO knowledge (id) VALUES (1);
INSERT INTO "knowledge" (id) VALUES (2);
insert into KNOWLEDGE (id) VALUES (3);
INSERT INTO sessions (id) VALUES (9);
COMMIT;`)
	if got := countInserts(stmts, "knowledge"); got != 3 {
		t.Fatalf("countInserts = %d, want 3", got)
	}
	if got := countInserts(stmts, "sessions"); got != 1 {
		t.Fatalf("countInserts(sessions) = %d, want 1", got)
	}
}

// skipForDrill — public 시퀀스 조작문만 제외(빠른 단위 검증).
func TestSkipForDrill(t *testing.T) {
	yes := []string{
		"CREATE SEQUENCE IF NOT EXISTS public.knowledge_id_seq;",
		"create sequence if not exists public.x_seq;",
		"SELECT setval('public.knowledge_id_seq', COALESCE((SELECT MAX(\"id\") FROM \"knowledge\"), 1));",
		"select  setval ('public.s', 1)",
		"-- header\nSELECT setval('public.s', 1)",
	}
	no := []string{
		"INSERT INTO knowledge (id) VALUES (1);",
		"DELETE FROM knowledge;",
		"SELECT setval('flint_drill.knowledge_id_seq', 1);",
		"CREATE SEQUENCE IF NOT EXISTS flint_drill.x_seq;",
	}
	for _, s := range yes {
		if !skipForDrill(s) {
			t.Fatalf("제외 대상 아님(원본 public 조작문): %q", s)
		}
	}
	for _, s := range no {
		if skipForDrill(s) {
			t.Fatalf("제외하면 안 됨: %q", s)
		}
	}
}

// 회귀(비간섭): 덤프의 setval('public.*')·CREATE SEQUENCE public.* 문이
// 드릴 재생에서 원본 public 시퀀스를 건드리지 않는다. 오래된 keep-N 덤프로
// 훈련해도 public 시퀀스가 과거 값으로 되감기지 않아야 한다(PK 충돌 방지).
func TestDrillDoesNotTouchPublicSequence(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()

	var before int
	if err := pool.QueryRow(ctx,
		`SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='knowledge_id_seq'`).Scan(&before); err != nil {
		t.Fatalf("sequence last_value: %v", err)
	}

	script := `CREATE SEQUENCE IF NOT EXISTS public.knowledge_id_seq;
CREATE TABLE "knowledge" ("id" integer, "topic" text);
BEGIN;
INSERT INTO "knowledge" ("id", "topic") VALUES (1, 'a');
INSERT INTO "knowledge" ("id", "topic") VALUES (2, 'b');
COMMIT;
SELECT setval('public.knowledge_id_seq', COALESCE((SELECT MAX("id") FROM "knowledge"), 1));`
	path := filepath.Join(t.TempDir(), "old-dump.sql")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunDrill(dsn, path)
	if err != nil {
		t.Fatalf("RunDrill: %v", err)
	}
	if !res.Ok || res.Expected != 2 || res.Restored != 2 {
		t.Fatalf("드릴 결과: %+v, want Ok true 2/2", res)
	}
	var after int
	if err := pool.QueryRow(ctx,
		`SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='knowledge_id_seq'`).Scan(&after); err != nil {
		t.Fatalf("sequence last_value(after): %v", err)
	}
	if after != before {
		t.Fatalf("public 시퀀스 훼손: last_value %d -> %d (드릴은 불가침)", before, after)
	}
	var schemas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil || schemas != 0 {
		t.Fatalf("flint_drill 잔존: %d err=%v", schemas, err)
	}
}

// 회귀: maintenance_runs 테이블 부재(마이그레이션 게이트 공백 DB) 시에도
// 드릴 성공·기록이 동작해야 한다(에러로 뭉개지지 않음).
func TestDrillWithoutMaintenanceRuns(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS public.maintenance_runs`); err != nil {
		t.Fatalf("drop maintenance_runs: %v", err)
	}

	script := `CREATE TABLE "knowledge" ("id" integer);
INSERT INTO "knowledge" ("id") VALUES (1);`
	path := filepath.Join(t.TempDir(), "mini.sql")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunDrill(dsn, path)
	if err != nil {
		t.Fatalf("maintenance_runs 부재 드릴: %v", err)
	}
	if !res.Ok || res.Expected != 1 || res.Restored != 1 {
		t.Fatalf("드릴 결과: %+v", res)
	}
	var runs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.maintenance_runs WHERE kind = 'drill'`).Scan(&runs); err != nil {
		t.Fatalf("maintenance_runs 재생성/조회: %v", err)
	}
	if runs < 1 {
		t.Fatalf("drill 행 미기록: %d", runs)
	}
}

// Review Focus(state): 실DB 덤프 재생 → Ok true·스키마 무잔존.
// Review Focus(numeric): 행수 대조 — Expected==Restored==실제 knowledge 행수.

// drillLock — flint_drill 스키마는 패키지 간(프로세스 간) 공유 자원이다.
// go test가 패키지를 병렬 실행하므로 파일 잠금으로 상호 배제한다.
func drillLock(t *testing.T) func() {
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

func TestDrillRestoresAndCleans(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dsn := testDSN()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer repo.Close()
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM knowledge WHERE content_hash LIKE 't55-drill-%'`)
	}
	// 개발 DB가 v0.4 이전에 만들어져 maintenance_runs이 없을 수 있다(마이그레이션
	// 게이트가 schema_version>=1이면 건너뛴다) — 스키마.sql 정의로 멱등 보장.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		t.Fatalf("ensure maintenance_runs: %v", err)
	}
	cleanup()
	defer cleanup()
	for i := 1; i <= 3; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO knowledge
			(topic, summary, tags, confidence, content_hash)
			VALUES ('drill', 't55 probe', ':t55:', 0.9, $1)`,
			"t55-drill-"+strings.Repeat("p", i)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// 타 패키지 테스트가 public 테이블을 동시 생성/삭제하면 덤프가 일시적으로
	// 오염될 수 있다 — 1회 재시도로 흡수한다(파일 잠금은 flint_drill 경합 방지용).
	var res DrillResult
	var runErr error
	before := 0
	for attempt := 0; attempt < 2; attempt++ {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&before); err != nil {
			t.Fatalf("count before: %v", err)
		}
		if before < 3 {
			t.Fatalf("프로브 미삽입: count=%d", before)
		}
		var dump bytes.Buffer
		if err := Dump(ctx, dsn, &dump); err != nil {
			t.Fatalf("dump: %v", err)
		}
		path := filepath.Join(t.TempDir(), "drill.sql")
		if err := os.WriteFile(path, dump.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		res, runErr = RunDrill(dsn, path)
		if runErr == nil {
			break
		}
	}
	if runErr != nil {
		t.Fatalf("RunDrill: %v", runErr)
	}
	if !res.Ok {
		t.Fatalf("Ok=false: %+v", res)
	}
	if res.Expected != before || res.Restored != before {
		t.Fatalf("행수 대조 실패: expected=%d restored=%d, want %d/%d",
			res.Expected, res.Restored, before, before)
	}

	// state — 스키마 무잔존.
	var schemas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("flint_drill 잔존: %d", schemas)
	}

	// 비간섭 — 원본 knowledge 행수 불변.
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("원본 훼손: before=%d after=%d", before, after)
	}

	// 성공 기록 — maintenance_runs 1행(kind=drill) + meta last_drill.
	var runs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM maintenance_runs WHERE kind = 'drill'`).Scan(&runs); err != nil {
		t.Fatalf("maintenance_runs: %v", err)
	}
	if runs < 1 {
		t.Fatalf("maintenance_runs drill 행 없음")
	}
	var lastISO string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'last_drill'`).Scan(&lastISO); err != nil {
		t.Fatalf("meta last_drill: %v", err)
	}
	last, err := time.Parse(time.RFC3339, lastISO)
	if err != nil {
		t.Fatalf("last_drill 파싱 불가 %q: %v", lastISO, err)
	}
	if time.Since(last) > 5*time.Minute {
		t.Fatalf("last_drill=%v, want 최근", last)
	}
}

// Review Focus(state): 재생 실패 시에도 flint_drill 잔존 금지.
func TestDrillFailureCleansSchema(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()

	path := filepath.Join(t.TempDir(), "broken.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE broken (a int;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunDrill(dsn, path)
	if err == nil {
		t.Fatalf("고장 덤프인데 에러 없음: %+v", res)
	}
	var schemas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("실패 후 flint_drill 잔존: %d", schemas)
	}
}
