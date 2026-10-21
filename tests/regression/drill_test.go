// drill — T55 restore --drill 회귀(코디네이터 룰링: lib/cli 소유 블록 동결로
// tests/regression/에 배치). cli.Run·backup 공개 계약 수준에서 Review Focus
// 2건(state: flint_drill 잔존 금지 — 실패 시에도 DROP, numeric: 행수 대조)을
// 실DB로 재검증한다. 내부 seam 케이스는 lib/backup/drill_test.go.
package regression

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/backup"
	"flint/lib/cli"
	"flint/lib/db"
)

// regDrillDSN — FLINT_DSN 우선, 없으면 기본(ADR-0001: 포트 5433).
func regDrillDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	return "postgresql://flint:flint-dev@localhost:5433/flint"
}

// regDrillDB — 실DB 접속. 미기동 시 스킵(회귀는 slow 계층).
func regDrillDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, regDrillDSN())
	if err != nil {
		t.Skipf("doltgres unavailable: %v", err)
	}
	return pool
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

// regDrillRun — cli.Run 공용 헬퍼.
func regDrillRun(args ...string) (string, int) {
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(""), &out)
	return out.String(), code
}

// 계획 지정 케이스 — 실DB 덤프 재생 → Ok true·스키마 무잔존.
func TestDrillRestoresAndCleans(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()

	// 개발 DB가 v0.4 이전 생성이면 maintenance_runs이 없을 수 있다(게이트 공백).
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		t.Fatalf("ensure maintenance_runs: %v", err)
	}
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM knowledge WHERE topic = 'reg-drill'`)
	}
	cleanup()
	defer cleanup()
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO knowledge
			(topic, summary, tags, confidence, content_hash)
			VALUES ('reg-drill', 'probe', ':r:', 0.9, $1)`,
			"reg-drill-"+strings.Repeat("p", i+1)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	// 타 패키지 테스트가 public 테이블을 동시 생성/삭제하면 덤프가 일시적으로
	// 오염될 수 있다 — 1회 재시도로 흡수한다(파일 잠금은 flint_drill 경합 방지용).
	var out string
	var code int
	before := 0
	for attempt := 0; attempt < 2; attempt++ {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&before); err != nil {
			t.Fatalf("count: %v", err)
		}
		var dump bytes.Buffer
		if err := backup.Dump(ctx, regDrillDSN(), &dump); err != nil {
			t.Fatalf("dump: %v", err)
		}
		path := filepath.Join(t.TempDir(), "drill.sql")
		if err := os.WriteFile(path, dump.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		out, code = regDrillRun("restore", "--drill", "--file", path)
		if code == 0 {
			break
		}
	}
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"status":"drill"`) || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("drill 출력=%q", out)
	}
	if !strings.Contains(out, `"expected":`+strconv.Itoa(before)) ||
		!strings.Contains(out, `"restored":`+strconv.Itoa(before)) {
		t.Fatalf("행수 대조 실패(기대 %d): %q", before, out)
	}

	// state — 스키마 무잔존 + 원본 불변.
	var schemas, after int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("flint_drill 잔존: %d", schemas)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("원본 훼손: before=%d after=%d", before, after)
	}

	// 성공 기록 — maintenance_runs(kind=drill) + meta last_drill 최근 값.
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
	if last, err := time.Parse(time.RFC3339, lastISO); err != nil || time.Since(last) > 5*time.Minute {
		t.Fatalf("last_drill=%q 파싱/경과 오류: %v", lastISO, err)
	}
}

// Review Focus(numeric) — INSERT 문 수와 재생 행수가 어긋나면 ok:false·exit 1.
// (중간 DELETE로 Expected=2, Restored=1인 덤프를 합성한다.)
func TestDrillMismatchExit1(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()
	pool.Exec(ctx, `DROP SCHEMA IF EXISTS flint_drill`)

	script := `CREATE TABLE "knowledge" ("id" integer);
BEGIN;
INSERT INTO "knowledge" ("id") VALUES (1);
DELETE FROM "knowledge";
INSERT INTO "knowledge" ("id") VALUES (2);
COMMIT;`
	path := filepath.Join(t.TempDir(), "mismatch.sql")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := regDrillRun("restore", "--drill", "--file", path)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, `"ok":false`) || !strings.Contains(out, `"expected":2`) ||
		!strings.Contains(out, `"restored":1`) {
		t.Fatalf("불일치 출력=%q", out)
	}
	var schemas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).
		Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("flint_drill 잔존: %d", schemas)
	}
}

// Review Focus(state) — 고장 덤프 재생 실패 시에도 flint_drill 잔존 금지.
func TestDrillFailureCleansSchema(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()
	pool.Exec(ctx, `DROP SCHEMA IF EXISTS flint_drill`)

	path := filepath.Join(t.TempDir(), "broken.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE broken (a int;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := regDrillRun("restore", "--drill", "--file", path)
	if code == 0 {
		t.Fatalf("고장 덤프인데 exit 0: %q", out)
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

// --drill 누락 --file — 사용법 에러 exit 1·한 줄 JSON.
func TestDrillRequiresFile(t *testing.T) {
	regDrillDB(t) // cli.Run이 store를 연다 — DB 미기동 시 스킵 용도
	out, code := regDrillRun("restore", "--drill")
	if code != 1 {
		t.Fatalf("exit=%d, want 1", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 JSON 아님: %q", out)
	}
}

// maintain --check — last_drill 30일 초과 시 drill 보고 추가(T55).
func TestMaintainCheckDrillReport(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()

	restore := func(v string) {
		_, err := pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('last_drill', $1)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v)
		if err != nil {
			t.Fatalf("meta 복원: %v", err)
		}
	}
	fresh := time.Now().UTC().Add(-29 * 24 * time.Hour).Format(time.RFC3339)
	old := time.Now().UTC().Add(-31 * 24 * time.Hour).Format(time.RFC3339)

	restore(fresh)
	defer restore(fresh)
	out, code := regDrillRun("maintain", "--check")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if strings.Contains(out, `"drill"`) {
		t.Fatalf("29일 경과에 drill 필드 불필요: %q", out)
	}

	restore(old)
	out, code = regDrillRun("maintain", "--check")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"drill":"stale"`) {
		var cur string
		pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = 'last_drill'`).Scan(&cur)
		t.Fatalf("31일 경과 drill 보고 없음: %q (그 순간 meta last_drill=%q)", out, cur)
	}
}
