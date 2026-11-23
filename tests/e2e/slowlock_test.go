//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"reflect"
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
