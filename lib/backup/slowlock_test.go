// slowlock_test.go — slow(실DB) 스위트의 교차 프로세스 직렬화. 드릴 락
// (flock)은 프로세스 간이지만 이 스위트 전체를 덮지 못한다: 락을 잡는
// 테스트와 안 잡는 타 패키지/e2e 하위 프로세스가 같은 DB에 동시 커밋해
// Doltgres 낙관적 충돌(40001)로 플레이크한다. TestMain이 전용 접속으로
// 세션 어드바이저리 락을 잡고 m.Run() 내내 유지해 DB 서버 단위로 상호
// 배제한다(파일시스템 경계·컨테이너 경계와 무관).
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

// slowLockKey — slow 스위트 직렬화 어드바이저리 락 키. 세션 락은 DB 단위
// 로 유효하므로 같은 DB를 쓰는 프로세스끼리만 상호 배제된다.
const slowLockKey = "flint_test_slow"

const (
	slowLockSQL   = `SELECT pg_advisory_lock(hashtext('` + slowLockKey + `'))`
	slowUnlockSQL = `SELECT pg_advisory_unlock(hashtext('` + slowLockKey + `'))`
)

// slowLockConn — 어드바이저리 락 SQL 발행 최소 세임(*pgx.Conn·fake 공통).
type slowLockConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var _ slowLockConn = (*pgx.Conn)(nil)

// acquireSlowLock — 세션 어드바이저리 락 획득(블로킹: 타 프로세스 slow
// 스위트가 종료해 해제할 때까지 대기 — 직렬화 본체).
func acquireSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowLockSQL)
	return err
}

// releaseSlowLock — 세션 어드바이저리 락 해제.
func releaseSlowLock(ctx context.Context, conn slowLockConn) error {
	_, err := conn.Exec(ctx, slowUnlockSQL)
	return err
}

// lockSlowSuite — dsn에 전용 접속을 열어 세션 어드바이저리 락을 잡고,
// 반환 함수에서 해제+접속 종료한다. 실DB 접속 실패(미기동·CI 마이그레이션
// 전)면 락 없이 진행해 기존 스킵 계약을 승계한다.
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
		// 해제에 실패해도 접속 종료로 세션 락은 서버가 회수한다.
		conn.Close(ctx)
	}
}

// TestMain — slow 스위트 전체를 어드바이저리 락으로 프로세스 간 직렬화
// (40001 플레이크 클래스 제거). os.Exit 전에 락을 해제한다.
func TestMain(m *testing.M) {
	release := lockSlowSuite(testDSN())
	code := m.Run()
	release()
	os.Exit(code)
}

// TestSlowLockUsesTestDSN — TestMain 직렬화(testDSN 경유)가 tests/와 동일한
// 테스트 DSN 계약(FLINT_TEST_DSN 우선·기본 flint_test)을 따름을 단얱.
// FLINT_DSN(운영)이 설정돼 있어도 무시해야 한다(운영 DB 접속 차단).
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

// fakeSlowConn — 락 SQL 발행 단얱용 fake 접속.
type fakeSlowConn struct {
	stmts []string
}

func (f *fakeSlowConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.stmts = append(f.stmts, sql)
	return pgconn.CommandTag{}, nil
}

// TestSlowLockIssuesAdvisorySQL — TestMain 직렬화 경로가 어드바이저리 락
// 획득·해제 SQL을 실제로 발행함을 고정(fake 접속 — 실DB 불필요).
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
