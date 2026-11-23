package regression

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

func TestLegacyV3UpgradeColumnsBeforeViews(t *testing.T) {
	regSharedDBLock(t)
	pool := regViewDB(t)
	defer pool.Close()
	ctx := context.Background()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "flint")
	build := exec.Command("go", "build", "-o", bin, "./cmd/flint")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build 실패: %v; out=%s", err, b)
	}
	runBin := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "FLINT_DSN="+regViewDSN())
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("실bin 실행: %v; out=%s", err, out.String())
			}
			code = ee.ExitCode()
		}
		return out.String(), code
	}

	topic := "reg-v3-upgrade"
	cleanup := func() { pool.Exec(ctx, `DELETE FROM knowledge WHERE topic = $1`, topic) }
	cleanup()
	defer cleanup()

	if out, code := runBin("read", topic); code != 0 {
		t.Fatalf("준비 read exit=%d, want 0(제로히트 — v1.5.3): %s", code, out)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge
		(topic, summary, tags, confidence, content_hash)
		VALUES ($1, 'v3 업그레이드 회귀', ':r:', 0.9, 'reg-v3-upgrade-v4')`, topic); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := pool.Exec(ctx, `DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 드롭: %v", err)
	}
	for _, col := range []string{"read_count", "last_read_at"} {
		if _, err := pool.Exec(ctx, `ALTER TABLE knowledge DROP COLUMN `+col); err != nil {
			t.Fatalf("%s 컬럼 드롭: %v", col, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE meta SET value = '3' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("버전 강등: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM meta WHERE key IN ('views_hash','views_sig')`); err != nil {
		t.Fatalf("뷰 지문 삭제: %v", err)
	}

	out, code := runBin("read", topic)
	if code != 0 {
		t.Fatalf("레거시 v3 read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) {
		t.Fatalf("read 출력에 seed 행 없음: %q", out)
	}

	var version string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if want := strconv.Itoa(db.LatestSchemaVersion); version != want {
		t.Fatalf("schema_version=%q, want %s", version, want)
	}

	rows, err := pool.Query(ctx, `SELECT * FROM active_knowledge LIMIT 0`)
	if err != nil {
		t.Fatalf("active_knowledge 조회: %v", err)
	}
	cols := map[string]bool{}
	for _, fd := range rows.FieldDescriptions() {
		cols[string(fd.Name)] = true
	}
	rows.Close()
	for _, c := range []string{"read_count", "last_read_at"} {
		if !cols[c] {
			t.Fatalf("active_knowledge에 %s 미노출: %v", c, cols)
		}
	}
}
