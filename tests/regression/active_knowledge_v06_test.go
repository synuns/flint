// active_knowledge_v06_test.go — T62 결함 회귀(코디네이터 룰링, progress.md
// "T62 결함"): schema v4 참조 계측 컬럼(read_count·last_read_at)이
// active_knowledge 뷰에 노출되지 않아 RankOrder(T68)의 last_read_at 정렬이
// 실DB에서 42703으로 실패, read가 exit 1. views.sql 말미 노출과 v0.5 이중
// 게이트(views_hash)의 재적용을 실DB 종단(cli.Run read)으로 검증한다.
// 기존 테스트 수정 금지 룰에 따라 신규 파일(드릴 패턴 계승).
package regression

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/cli"
	"flint/lib/db"
)

// regViewDSN — FLINT_DSN 우선, 없으면 기본(ADR-0001: 포트 5433).
func regViewDSN() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	return "postgresql://flint:flint-dev@localhost:5433/flint"
}

// regViewDB — 실DB 접속. 미기동 시 스킵(회귀는 slow 계층).
func regViewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, regViewDSN())
	if err != nil {
		t.Skipf("doltgres unavailable: %v", err)
	}
	return pool
}

// regSharedDBLock — 공유 Doltgres DB를 쓰는 패키지 간 상호 배제(파일
// 잠금). go test ./...는 패키지 바이너리를 병렬로 띄우므로 잠금 없이는
// lib/db의 useFakeSQL(fake active_knowledge 뷰 재적용)이 이 테스트의
// read와 겹쳐 42703(column confidence)으로 실패한다. 잠금 파일 경로는
// lib/db repo_test.go sharedDBLock·lib/backup drillLock과 동일해야
// 상호 배제가 성립한다(v0.5 선례).
func regSharedDBLock(t *testing.T) {
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
	t.Cleanup(func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// TestActiveKnowledgeExposesReadColumns — read가 Migrate(뷰 게이트)를
// 거친 뒤 exit 0이고, 뷰가 v4 컬럼 2개를 노출하는지. 낡은 뷰(컬럼
// 미노출)에서는 ORDER BY last_read_at이 42703이 되어 이 테스트는 실패한다.
func TestActiveKnowledgeExposesReadColumns(t *testing.T) {
	regSharedDBLock(t)
	pool := regViewDB(t)
	defer pool.Close()
	ctx := context.Background()

	// seed — 조회 대상 행. ORDER BY의 42703은 행수와 무관하게 발생하지만,
	// 출력 검증으로 조회 경로 전체(read → 뷰 → 출력)를 잡는다.
	cleanup := func() { pool.Exec(ctx, `DELETE FROM knowledge WHERE topic = 'reg-view-read'`) }
	cleanup()
	defer cleanup()
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge
		(topic, summary, tags, confidence, content_hash)
		VALUES ('reg-view-read', 'view regression', ':r:', 0.9, 'reg-view-read-v4')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 마이그레이션 후 read exit 0 — cli.Run이 newRealStore→Migrate를 수행,
	// views_hash 불일치(views.sql 갱신 배포) 시 뷰를 재적용한다.
	t.Setenv("FLINT_DSN", regViewDSN())
	var out bytes.Buffer
	if code := cli.Run([]string{"read", "reg-view-read"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read exit=%d, want 0 (Migrate 후); out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "reg-view-read") {
		t.Fatalf("read 출력에 seed 행 없음: %q", out.String())
	}

	// 뷰 컬럼 계약 — SELECT * 필드명에 v4 2컬럼 존재.
	rows, err := pool.Query(ctx, `SELECT * FROM active_knowledge LIMIT 0`)
	if err != nil {
		t.Fatalf("view query: %v", err)
	}
	cols := make(map[string]bool)
	for _, fd := range rows.FieldDescriptions() {
		cols[string(fd.Name)] = true
	}
	rows.Close()
	for _, c := range []string{"read_count", "last_read_at"} {
		if !cols[c] {
			t.Fatalf("active_knowledge에 %s 컬럼 미노출: %v", c, cols)
		}
	}
}
