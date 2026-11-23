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

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
)

func regTestDSN() string {
	return testengine.ResolveDSN()
}

func ensureTestDB(ctx context.Context) error {
	testDSN := regTestDSN()
	if pool, err := db.Connect(ctx, testDSN); err == nil {
		pool.Close()
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

	release := lockSlowSuite(regTestDSN())
	code := m.Run()
	release()
	os.Exit(code)
}

func TestRegTestDSNDefaultsToIsolatedDB(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	t.Setenv("FLINT_TEST_DSN", "")
	if got := regTestDSN(); got != testengine.TestDSNDefault() {
		t.Errorf("regTestDSN = %q, want 전용 엔진 %q(운영 DSN 무시)", got, testengine.TestDSNDefault())
	}
}

func TestRegTestDSNEnvOverride(t *testing.T) {
	t.Setenv("FLINT_TEST_DSN", "postgresql://flint:t@h:5434/flint_ci")
	if got := regTestDSN(); got != "postgresql://flint:t@h:5434/flint_ci" {
		t.Errorf("regTestDSN = %q, want FLINT_TEST_DSN 우선", got)
	}
}
