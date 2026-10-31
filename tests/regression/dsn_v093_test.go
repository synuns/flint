// dsn_v093_test.go — v0.9.3 R1 테스트 격리: 회귀·e2e의 DSN 폴백이 운영
// DB(FLINT_DSN → flint)로 직결해 rv-* 정크(id 63·64·65·108·109)를 남긴
// 경로를 끊는다. 이 패키지는 FLINT_TEST_DSN(기본 flint_test)만 쓰며
// FLINT_DSN(운영)은 무시한다. TestMain이 flint_test 부재 시 생성(postgres→
// flint 순 admin 접속) 후 Repo.Migrate까지 마친다. Doltgres가 CREATE
// DATABASE를 지원하지 않으면 early-fail(v0.7 조기 실패 원칙 — 조용한
// 스킵 아님).
package regression

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

// regTestDefaultDSN — ADR-0010 포트 5434 위 격리 DB flint_test(v0.9.3 R1).
const regTestDefaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint_test"

// regTestDSN — FLINT_TEST_DSN 우선, 없으면 flint_test 기본. FLINT_DSN(운영)은
// 이 패키지에서 무시한다.
func regTestDSN() string {
	if d := os.Getenv("FLINT_TEST_DSN"); d != "" {
		return d
	}
	return regTestDefaultDSN
}

// ensureTestDB — flint_test 부재 시 생성(postgres→flint 순 admin 접속,
// 이미 존재 42P04는 관용 — 스파이크 검증: Doltgres CREATE DATABASE 지원)
// 후 Repo.Migrate. 서버 미응답은 db.ErrServerDown 래핑으로 돌려 TestMain이
// 기존 스킵 계층(회귀는 slow 레이어)을 유지하게 한다.
func ensureTestDB(ctx context.Context) error {
	testDSN := regTestDSN()
	if pool, err := db.Connect(ctx, testDSN); err == nil {
		pool.Close() // 이미 존재 — 아래 Migrate가 멱등 확인(최신 뷰 지문이면 no-op)
	} else {
		u, err := url.Parse(testDSN)
		if err != nil {
			return fmt.Errorf("FLINT_TEST_DSN 파싱: %w", err)
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
	}
	repo, err := db.NewRepo(ctx, testDSN)
	if err != nil {
		return err
	}
	defer repo.Close()
	return repo.Migrate(ctx)
}

// TestMain — flint_test 생성+Migrate(잠금은 regSharedDBLock과 동일 파일로
// 패키지 간 생성 경쟁 상호 배제) 후 CLI 기본 접속(FLINT_DSN)을 테스트 DSN으로
// 고정한다. cli.Run 및 os.Environ()을 물린 하위 프로세스가 운영 DB로 새는
// 경로를 원천 차단한다(R1).
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
	os.Setenv("FLINT_DSN", regTestDSN())
	// slow 스위트 전체를 세션 어드바이저리 락으로 교차 프로세스 직렬화
	// (Doltgres 40001 플레이크 — slowlock_test.go).
	release := lockSlowSuite(regTestDSN())
	code := m.Run()
	release()
	os.Exit(code)
}

func TestRegTestDSNDefaultsToIsolatedDB(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	t.Setenv("FLINT_TEST_DSN", "")
	if got := regTestDSN(); got != "postgresql://flint:flint-dev@localhost:5434/flint_test" {
		t.Errorf("regTestDSN = %q, want 운영 DSN 무시 + flint_test 기본값", got)
	}
}

func TestRegTestDSNEnvOverride(t *testing.T) {
	t.Setenv("FLINT_TEST_DSN", "postgresql://flint:t@h:5434/flint_ci")
	if got := regTestDSN(); got != "postgresql://flint:t@h:5434/flint_ci" {
		t.Errorf("regTestDSN = %q, want FLINT_TEST_DSN 우선", got)
	}
}
