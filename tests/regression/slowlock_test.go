package regression

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const slowLockKey = "flint_test_slow"

const (
	slowLockSQL   = `SELECT pg_advisory_lock(hashtext('` + slowLockKey + `'))`
	slowUnlockSQL = `SELECT pg_advisory_unlock(hashtext('` + slowLockKey + `'))`
)

type slowLockConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var _ slowLockConn = (*pgx.Conn)(nil)

func acquireSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowLockSQL)
	return err
}

func releaseSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowUnlockSQL)
	return err
}

func lockSlowSuite(dsn string) func() {
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(connectCtx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "slow 스위트 락 생략(실DB 접속 실패): %v\n", err)
		return func() {}
	}
	if err := acquireSlowLock(context.Background(), conn); err != nil {
		fmt.Fprintf(os.Stderr, "slow 스위트 락 획득 실패(계속): %v\n", err)
		conn.Close(context.Background())
		return func() {}
	}
	return func() {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := releaseSlowLock(ctx, conn); err != nil {
			fmt.Fprintf(os.Stderr, "slow 스위트 락 해제 실패: %v\n", err)
		}

		conn.Close(ctx)
	}
}

type fakeSlowConn struct {
	stmts []string
}

func (f *fakeSlowConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.stmts = append(f.stmts, sql)
	return pgconn.CommandTag{}, nil
}

func TestSlowLockIssuesAdvisorySQL(t *testing.T) {
	var conn fakeSlowConn
	if err := acquireSlowLock(context.Background(), &conn); err != nil {
		t.Fatalf("acquireSlowLock: %v", err)
	}
	if err := releaseSlowLock(context.Background(), &conn); err != nil {
		t.Fatalf("releaseSlowLock: %v", err)
	}
	want := []string{
		`SELECT pg_advisory_lock(hashtext('flint_test_slow'))`,
		`SELECT pg_advisory_unlock(hashtext('flint_test_slow'))`,
	}
	if !reflect.DeepEqual(conn.stmts, want) {
		t.Fatalf("발행 SQL = %q, want %q", conn.stmts, want)
	}
}

func TestIdempotencyContractKept(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "lib", "db", "admin_versions_test.go"))
	if err != nil {
		t.Fatalf("admin_versions_test.go 소스 조회: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"errs := make([]error, 5)",
		"for i := 0; i < 5; i++",
		"go func(i int)",
		"ON CONFLICT (from_id, to_id, relation) DO NOTHING",
		"func retryParallelInsert",
		"XX000",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("병렬 INSERT 멱등 계약 재현물 상실 — %q 없음 (T1 진단 R-2: 5-goroutine 계약 유지 필수)", want)
		}
	}
	for _, fn := range []string{
		"func TestSlowParallelLinkInsertSingleRowV1201",
		"func TestSlowSuiteSerialGreen",
	} {
		start := strings.Index(src, fn)
		if start < 0 {
			t.Errorf("slow 계약 테스트 부재 — %s", fn)
			continue
		}
		body := src[start:]
		if end := strings.Index(body, "\nfunc "); end >= 0 {
			body = body[:end]
		}
		if strings.Contains(body, "t.Skip") {
			t.Errorf("%s 본문에 t.Skip — 전체 skip 금지(ruling ②)", fn)
		}
	}
}
