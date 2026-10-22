package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

	// 상태 판정(헤르메틱, CI run 39713526850): 마이그레이션 전 신규 DB에서는
	// public.knowledge_id_seq가 아예 없다(pg_sequences 0행) — 미사용·부재는
	// 정상 조건으로 수용하고 "드릴 전후 상태 불변"으로 비간섭을 판정한다.
	// 시퀀스가 있으면 되감기 검증이 성립하도록 스스로 초기화한다(기존 최대
	// id보다 항상 앞인 값 — 잔여 DB의 이후 INSERT도 안전). 테스트가 시퀀스를
	// 새로 만들지는 않는다: 이후 마이그레이션의 CREATE TABLE ... serial이
	// 시퀀스 자동 생성과 충돌(42P07)하기 때문.
	probe := func() (bool, int) {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('public.knowledge_id_seq') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatalf("sequence 존재 확인: %v", err)
		}
		if !exists {
			return false, 0
		}
		var last int
		if err := pool.QueryRow(ctx,
			`SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='knowledge_id_seq'`).Scan(&last); err != nil {
			t.Fatalf("sequence last_value: %v", err)
		}
		return true, last
	}
	seed := func() int {
		exists, _ := probe()
		if !exists {
			return 0
		}
		base := 0
		var hasKn bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('public.knowledge') IS NOT NULL`).Scan(&hasKn); err != nil {
			t.Fatalf("knowledge 존재 확인: %v", err)
		}
		if hasKn {
			// 스칼라 서브쿼리 형태여야 한다 — Doltgres는 빈 테이블의
			// bare 집계(MAX)가 0행을 반환한다(Postgres는 NULL 1행).
			if err := pool.QueryRow(ctx,
				`SELECT COALESCE((SELECT MAX("id") FROM "knowledge"), 0)`).Scan(&base); err != nil {
				t.Fatalf("knowledge max id: %v", err)
			}
		}
		if _, err := pool.Exec(ctx,
			`SELECT setval('public.knowledge_id_seq', $1::bigint)`, int64(base+7)); err != nil {
			t.Fatalf("sequence 초기화 setval: %v", err)
		}
		return base + 7
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

	// 재시도 1회 허용: 병렬 패키지의 마이그레이션이 드릴 사이에 시퀀스를
	// 만들면 부재→존재로 보일 수 있다(잠금 밖 lib/cli Migrate). 진짜 회귀
	// (드릴이 setval·CREATE SEQUENCE public.*을 실행)는 재시도에서도
	// 불일치로 잡힌다 — 되감기 값은 결정적(2)이라 base+7과 항상 다르다.
	var before, after int
	var beforeExists, afterExists bool
	for attempt := 0; attempt < 2; attempt++ {
		before = seed()
		beforeExists, _ = probe()

		res, err := RunDrill(dsn, path)
		if err != nil {
			t.Fatalf("RunDrill: %v", err)
		}
		if !res.Ok || res.Expected != 2 || res.Restored != 2 {
			t.Fatalf("드릴 결과: %+v, want Ok true 2/2", res)
		}
		afterExists, after = probe()
		if beforeExists == afterExists && (!beforeExists || before == after) {
			break
		}
	}
	if beforeExists != afterExists || (beforeExists && after != before) {
		t.Fatalf("public 시퀀스 훼손: exists %v(last %d) -> %v(last %d) (드릴은 불가침)",
			beforeExists, before, afterExists, after)
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

// 회귀(커넥션 잔류): 드릴 재생이 flint_drill로 바꾼 search_path를 풀 반납
// 커넥션에 남기지 않는다. MaxConns=1 풀에서 drillConn 실행 전후 같은
// 커넥션으로 current_setting을 읽어 잔류 여부를 검증한다(잔류 시 이후
// 비한정 쿼리가 public 테이블을 못 찾는다 — CI PR #1 42P01).
func TestDrillConnResetsSearchPath(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(testDSN())
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = 1 // 반납 후 재취득이 같은 커넥션이 되게 강제
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := drillConn(ctx, pool, func(conn *pgxpool.Conn) error {
		var sp string
		if err := conn.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&sp); err != nil {
			return err
		}
		if !strings.Contains(sp, DrillSchema) {
			return fmt.Errorf("드릴 커넥션 search_path=%q, want %q 포함", sp, DrillSchema)
		}
		return nil
	}); err != nil {
		t.Fatalf("drillConn: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&after); err != nil {
		t.Fatalf("search_path(after): %v", err)
	}
	if strings.Contains(after, DrillSchema) || !strings.Contains(after, "public") {
		t.Fatalf("search_path 잔류: %q, want public", after)
	}
}

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

	// 헤르메틱 대조: 사전 시딩(로컬 잔류 데이터)에 의존하지 않고 매 시도마다
	// 자체 행을 심는다. 잠금 없는 타 패키지 테스트가 count와 dump 사이에
	// knowledge를 비우면 행수가 어긋난다(CI PR #1: expected=1 restored=1,
	// want 12/12) — 에러뿐 아니라 불일치도 재시도로 흡수한다.
	var res DrillResult
	var before int
	var runErr error
	for attempt := 0; attempt < 3; attempt++ {
		for i := 1; i <= 3; i++ {
			if _, err := pool.Exec(ctx, `INSERT INTO knowledge
				(topic, summary, tags, confidence, content_hash)
				VALUES ('drill', 't55 probe', ':t55:', 0.9, $1)`,
				fmt.Sprintf("t55-drill-a%d-%s", attempt, strings.Repeat("p", i))); err != nil {
				t.Fatalf("seed %d: %v", i, err)
			}
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&before); err != nil {
			t.Fatalf("count before: %v", err)
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
		if runErr == nil && res.Ok && res.Expected == before && res.Restored == before {
			break // 일관된 스냅샷 — 대조 성립
		}
	}
	if runErr != nil {
		t.Fatalf("RunDrill: %v", runErr)
	}
	if !res.Ok {
		t.Fatalf("Ok=false: %+v", res)
	}
	if before < 3 {
		t.Fatalf("프로브 미삽입: count=%d", before)
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
