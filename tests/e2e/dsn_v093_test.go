//go:build e2e

// dsn_v093_test.go — v0.9.3 R1 테스트 격리(e2e측): dsn()은 FLINT_TEST_DSN
// (기본 flint_test)만 쓰고 FLINT_DSN(운영)은 무시한다. TestMain이
// flint_test 부재 시 생성 후 하위 프로세스 환경(os.Environ 전파)의
// FLINT_DSN까지 테스트 DSN으로 고정한다 — 운영 DB 재오염 경로 차단.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/db"
)

// ensureTestDB — flint_test 부재 시 생성(postgres→flint 순 admin 접속,
// 이미 존재 42P04 관용) 후 스키마 확보. 각 체인 테스트가 NewRepo+Migrate
// 하므로 여기선 DB 존재까지만 보장한다(회귀 패키지 dsn_v093_test.go와
// 동일 논리 — 빌드 태그로 패키지 공유가 불가해 중복).
func ensureTestDB(ctx context.Context) error {
	testDSN := dsn()
	if pool, err := db.Connect(ctx, testDSN); err == nil {
		pool.Close()
		return nil
	}
	u, err := url.Parse(testDSN)
	if err != nil {
		return fmt.Errorf("DSN 파싱: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	var admin *pgxpool.Pool
	var lastErr error
	for _, adminName := range []string{"postgres", "flint"} {
		u.Path = "/" + adminName
		if p, e := db.Connect(ctx, u.String()); e == nil {
			admin = p
			break
		} else {
			lastErr = e
		}
	}
	if admin == nil {
		return fmt.Errorf("admin 접속 실패: %w", lastErr)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx,
		"CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P04" {
			return fmt.Errorf("CREATE DATABASE %s 실패(Doltgres 미지원 가능): %w", name, err)
		}
	}
	return nil
}

// TestMain — flint_test 확보(잠금은 lib/db sharedDBLock·회귀 패키지와 동일
// 파일로 패키지 간 생성 경쟁 상호 배제) 후 FLINT_DSN을 테스트 DSN으로
// 고정한다. os.Environ()을 그대로 물리는 하위 프로세스(hot·parallel 등)가
// 운영 DB로 새는 경로를 원천 차단한다(R1).
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	f, lockErr := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if lockErr == nil {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			fmt.Fprintf(os.Stderr, "flint_test 잠금 실패(계속): %v\n", err)
		}
	}
	ensureErr := ensureTestDB(ctx)
	cancel()
	if lockErr == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
	if ensureErr != nil {
		if errors.Is(ensureErr, db.ErrServerDown) {
			fmt.Fprintf(os.Stderr,
				"flint_test 준비 생략(Doltgres 미응답): %v — 이 패키지 테스트는 스킵됨\n", ensureErr)
		} else {
			fmt.Fprintf(os.Stderr, "flint_test 준비 실패(early-fail): %v\n", ensureErr)
			os.Exit(1)
		}
	}
	os.Setenv("FLINT_DSN", dsn())
	// slow 스위트 전체를 세션 어드바이저리 락으로 교차 프로세스 직렬화
	// (Doltgres 40001 플레이크 — slowlock_test.go).
	release := lockSlowSuite(dsn())
	code := m.Run()
	release()
	os.Exit(code)
}

// TestE2EDSNIsolated — Review Focus(명명): e2e dsn() 기본값이 flint_test.
func TestE2EDSNIsolated(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	t.Setenv("FLINT_TEST_DSN", "")
	if got := dsn(); got != "postgresql://flint:flint-dev@localhost:5434/flint_test" {
		t.Errorf("dsn = %q, want 운영 DSN 무시 + flint_test 기본값", got)
	}
}
