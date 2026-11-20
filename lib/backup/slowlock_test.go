// slowlock_test.go — cross-process serialization of the slow (real-DB)
// suite. The drill lock (flock) is cross-process but does not cover this
// whole suite: tests that hold the lock and other packages/e2e subprocesses
// that don't can commit to the same DB concurrently and flake on Doltgres
// optimistic conflicts (40001). TestMain takes a session advisory lock on a
// dedicated connection and holds it throughout m.Run(), mutually excluding
// at the DB-server level (regardless of filesystem or container
// boundaries).
package backup

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

// slowLockKey — the advisory lock key serializing the slow suite. A session
// lock is valid per DB, so only processes using the same DB exclude one
// another.
const slowLockKey = "flint_test_slow"

const (
	slowLockSQL   = `SELECT pg_advisory_lock(hashtext('` + slowLockKey + `'))`
	slowUnlockSQL = `SELECT pg_advisory_unlock(hashtext('` + slowLockKey + `'))`
)

// slowLockConn — the minimal seam for issuing advisory lock SQL (shared by *pgx.Conn and fakes).
type slowLockConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var _ slowLockConn = (*pgx.Conn)(nil)

// acquireSlowLock — acquires the session advisory lock (blocking: waits
// until another process's slow suite finishes and releases it — the actual
// serialization).
func acquireSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowLockSQL)
	return err
}

// releaseSlowLock — releases the session advisory lock.
func releaseSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowUnlockSQL)
	return err
}

// lockSlowSuite — opens a dedicated connection to dsn, takes the session
// advisory lock, and the returned function releases it and closes the
// connection. If the real-DB connection fails (not started, or CI before
// migration), it proceeds without the lock, inheriting the existing skip
// contract.
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
		// Even if release fails, closing the connection lets the server reclaim the session lock.
		conn.Close(ctx)
	}
}

// TestMain — serializes the whole slow suite across processes with the
// advisory lock (eliminating the 40001 flake class). Releases the lock
// before os.Exit.
func TestMain(m *testing.M) {
	release := lockSlowSuite(testDSN())
	code := m.Run()
	release()
	os.Exit(code)
}

// TestSlowLockUsesTestDSN — asserts that the TestMain serialization (via
// testDSN) follows the same test-DSN contract as tests/ (FLINT_TEST_DSN
// first, default flint_test). Even with FLINT_DSN (production) set, it must
// be ignored (blocking connections to the production DB).
func TestSlowLockUsesTestDSN(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	t.Setenv("FLINT_TEST_DSN", "postgresql://flint:t@h:5434/flint_ci")
	if got := testDSN(); got != "postgresql://flint:t@h:5434/flint_ci" {
		t.Errorf("testDSN = %q, want FLINT_TEST_DSN 우선", got)
	}
	t.Setenv("FLINT_TEST_DSN", "")
	if got := testDSN(); got != "postgresql://flint:flint-dev@localhost:5434/flint_test" {
		t.Errorf("기본 testDSN = %q, want flint_test(FLINT_DSN 무시)", got)
	}
}

// fakeSlowConn — a fake connection for asserting the issued lock SQL.
type fakeSlowConn struct {
	stmts []string
}

func (f *fakeSlowConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.stmts = append(f.stmts, sql)
	return pgconn.CommandTag{}, nil
}

// TestSlowLockIssuesAdvisorySQL — pins that the TestMain serialization path
// actually issues the advisory lock acquire/release SQL (fake connection —
// no real DB needed).
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
