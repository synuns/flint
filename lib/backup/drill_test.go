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

	"github.com/jackc/pgx/v5"
	"github.com/synuns/flint/lib/db"
)

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
			`SELECT COALESCE(last_value, 0) FROM pg_sequences WHERE schemaname='public' AND sequencename='knowledge_id_seq'`).Scan(&last); err != nil {
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

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.meta (
		key   text PRIMARY KEY,
		value text NOT NULL)`); err != nil {
		t.Fatalf("ensure meta: %v", err)
	}

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
	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil || dbs != 0 {
		t.Fatalf("%s 잔존: %d err=%v", DrillDB, dbs, err)
	}
}

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

func TestDrillReplayDoesNotContaminateOriginalPool(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(testDSN())
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var before string
	if err := pool.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&before); err != nil {
		t.Fatalf("search_path(before): %v", err)
	}

	script := `CREATE TABLE "knowledge" ("id" integer);
INSERT INTO "knowledge" ("id") VALUES (1);`
	path := filepath.Join(t.TempDir(), "mini.sql")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunDrill(testDSN(), path); err != nil {
		t.Fatalf("RunDrill: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `SELECT current_setting('search_path')`).Scan(&after); err != nil {
		t.Fatalf("search_path(after): %v", err)
	}
	if after != before || strings.Contains(after, DrillDB) || !strings.Contains(after, "public") {
		t.Fatalf("search_path 오염: before=%q after=%q, want 불변(public)", before, after)
	}
}

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

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		t.Fatalf("ensure maintenance_runs: %v", err)
	}
	cleanup()
	defer cleanup()

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
			break
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

	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 잔존: %d", DrillDB, dbs)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("원본 훼손: before=%d after=%d", before, after)
	}

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
	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("실패 후 %s 잔존: %d", DrillDB, dbs)
	}
}

func dropInjectScaffold(t *testing.T) (dsn, path string, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dsn = testDSN()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err = db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge
		(topic, summary, tags, confidence, content_hash)
		VALUES ('drill', 'drop probe', ':t55:', 0.9, 't55-drop-a1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var dump bytes.Buffer
	if err := Dump(ctx, dsn, &dump); err != nil {
		t.Fatalf("dump: %v", err)
	}
	path = filepath.Join(t.TempDir(), "drill.sql")
	if err := os.WriteFile(path, dump.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dsn, path, pool
}

func TestDrillDropRetriesTransientFailure(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	dsn, path, pool := dropInjectScaffold(t)
	defer pool.Exec(context.Background(),
		`DELETE FROM knowledge WHERE content_hash LIKE 't55-drop-%'`)

	calls := 0
	saved := dropDrillDBFn
	dropDrillDBFn = func(ctx context.Context, p *pgxpool.Pool) error {
		calls++
		if calls == 2 {
			return fmt.Errorf("주입 40001: serialization failure")
		}
		return saved(ctx, p)
	}
	t.Cleanup(func() { dropDrillDBFn = saved })

	res, err := RunDrill(dsn, path)
	if err != nil {
		t.Fatalf("RunDrill(일시 DROP 실패): %v", err)
	}
	if !res.Ok {
		t.Fatalf("Ok=false: %+v", res)
	}
	if calls < 3 {
		t.Fatalf("재시도 미발생: drop 호출 %d회, want ≥3(선행+실패+재시도)", calls)
	}
	var dbs int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 잔존: %d", DrillDB, dbs)
	}
}

func TestDrillIsolatedInDedicatedDatabase(t *testing.T) {
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

	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+DrillDB); err != nil {
		t.Fatalf("잔존 조립 전 정리: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+DrillDB); err != nil {
		t.Fatalf("잔존 DB 생성: %v", err)
	}
	remnantCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	remnantCfg.Database = DrillDB
	remnant, err := pgx.ConnectConfig(ctx, remnantCfg)
	if err != nil {
		t.Fatalf("잔존 DB 접속: %v", err)
	}
	if _, err := remnant.Exec(ctx, `CREATE TABLE sentinel (id int)`); err != nil {
		remnant.Close(context.Background())
		t.Fatalf("감시 테이블: %v", err)
	}
	_, _ = remnant.Exec(ctx, `INSERT INTO sentinel (id) VALUES (1)`)
	if err := remnant.Close(context.Background()); err != nil {
		t.Fatalf("잔존 커넥션 종료: %v", err)
	}

	script := `CREATE TABLE "knowledge" ("id" integer);
INSERT INTO "knowledge" ("id") VALUES (1);`
	path := filepath.Join(t.TempDir(), "mini.sql")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RunDrill(dsn, path)
	if err != nil {
		t.Fatalf("RunDrill: %v", err)
	}
	if !res.Ok || res.Expected != 1 || res.Restored != 1 {
		t.Fatalf("드릴 결과: %+v, want Ok true 1/1", res)
	}

	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 잔존: %d", DrillDB, dbs)
	}

	var schemas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("flint_drill 스키마 잔존: %d", schemas)
	}
}

func TestDrillDropExhaustionFailsExplicitly(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	dsn, path, pool := dropInjectScaffold(t)
	defer pool.Exec(context.Background(),
		`DELETE FROM knowledge WHERE content_hash LIKE 't55-drop-%'`)

	calls := 0
	saved := dropDrillDBFn
	dropDrillDBFn = func(ctx context.Context, p *pgxpool.Pool) error {
		calls++
		if calls > 1 {
			return fmt.Errorf("주입 40001: serialization failure")
		}
		return saved(ctx, p)
	}
	t.Cleanup(func() { dropDrillDBFn = saved })

	res, err := RunDrill(dsn, path)
	if err == nil {
		t.Fatalf("DROP 소진인데 성공 반환: %+v", res)
	}
	if !res.Ok {
		t.Fatalf("드릴 자체는 성공이어야 함(실패 승격 판정 전제): %+v", res)
	}
	if !strings.Contains(err.Error(), DrillDB) {
		t.Fatalf("에러가 잔존 드릴 DB를 밝히지 않음: %v", err)
	}
	if calls < 4 {
		t.Fatalf("재시도 소진 미관측: drop 호출 %d회, want ≥4(선행+3회 시도)", calls)
	}

	if derr := saved(context.Background(), pool); derr != nil {
		t.Fatalf("잔존 정리: %v", derr)
	}
}
