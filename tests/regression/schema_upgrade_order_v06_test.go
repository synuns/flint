// schema_upgrade_order_v06_test.go — 전체 리뷰 파인딩 #1(배포 차단) 회귀:
// newRealStore가 Migrate(뷰 재적용, v4 컬럼 참조)를 EnsureSchemaV4(컬럼
// 추가)보다 먼저 돌리면 레거시 v3 DB 첫 접속에서 CREATE VIEW가 42703으로
// 실패해 재시도를 소진, 전 커맨드가 exit 1하고 뷰 DROP만 남아 구형 CLI도
// 파손된다. 컬럼 승격 → 뷰 재적용 순서를 실bin 커맨드로 검증한다.
package regression

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyV3UpgradeColumnsBeforeViews — 레거시 v3 상태(read_count·
// last_read_at 컬럼 부재·schema_version=3·뷰 미기록)에서 실bin read가
// exit 0·스키마 4 승격·뷰 v4 컬럼 노출을 만족하는지.
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

	// 준비 1 — 현행 스키마 구축(실bin 1회: Migrate+게이트) 후 seed.
	if out, code := runBin("read", topic); code != 0 {
		t.Fatalf("준비 read exit=%d: %s", code, out)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge
		(topic, summary, tags, confidence, content_hash)
		VALUES ($1, 'v3 업그레이드 회귀', ':r:', 0.9, 'reg-v3-upgrade-v4')`, topic); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 준비 2 — 레거시 v3 재현: 뷰 제거(컬럼 의존 정리) → v4 컬럼 제거 →
	// 버전 3·뷰 지문 삭제(views_hash 결측 = Migrate 재적용 트리거).
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

	// 본문 — 레거시 v3 첫 접속: 컬럼 승격이 뷰 재적용보다 먼저면 exit 0.
	// 순서가 뒤집혀 있으면 CREATE VIEW 42703 → 재시도 소진 exit 1.
	out, code := runBin("read", topic)
	if code != 0 {
		t.Fatalf("레거시 v3 read exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, topic) {
		t.Fatalf("read 출력에 seed 행 없음: %q", out)
	}

	// 승격 단언 — schema_version=5(v0.8: V5 게이트 체인 추가로 레거시 v3 DB는
	// v3→v4→v5 전부 승격. 컬럼-뷰 순서 회귀 아님 — 아래 read·뷰 단언은 그대로).
	var version string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if version != "5" {
		t.Fatalf("schema_version=%q, want 5", version)
	}

	// 뷰 단언 — active_knowledge가 v4 컬럼 2종을 노출.
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
