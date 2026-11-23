package regression

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
)

func regDrillDSN() string {
	return regTestDSN()
}

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

func regDrillRun(args ...string) (string, int) {
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestDrillRestoresAndCleans(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()

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

		d := sha256.Sum256(dump.Bytes())
		if err := backup.WriteSidecar(path, hex.EncodeToString(d[:])); err != nil {
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

	var dbs, after int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+backup.DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 잔존: %d", backup.DrillDB, dbs)
	}
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
	if last, err := time.Parse(time.RFC3339, lastISO); err != nil || time.Since(last) > 5*time.Minute {
		t.Fatalf("last_drill=%q 파싱/경과 오류: %v", lastISO, err)
	}
}

func TestDrillMismatchExit1(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()

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

	md := sha256.Sum256([]byte(script))
	if err := backup.WriteSidecar(path, hex.EncodeToString(md[:])); err != nil {
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
	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+backup.DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 잔존: %d", backup.DrillDB, dbs)
	}
}

func TestDrillFailureCleansSchema(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	pool := regDrillDB(t)
	defer pool.Close()
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "broken.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE broken (a int;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := regDrillRun("restore", "--drill", "--file", path)
	if code == 0 {
		t.Fatalf("고장 덤프인데 exit 0: %q", out)
	}
	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+backup.DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("실패 후 %s 잔존: %d", backup.DrillDB, dbs)
	}
}

func TestDrillRequiresFile(t *testing.T) {
	regDrillDB(t)
	out, code := regDrillRun("restore", "--drill")

	if code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("에러 JSON 아님: %q", out)
	}
}

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
