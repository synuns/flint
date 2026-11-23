package testengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

var composeUpFn func() error = defaultComposeUp

var dropDBFn func(ctx context.Context, name string) error = defaultDropDB

var purgeFn func(ctx context.Context) error = defaultPurge

var (
	engineMu      sync.Mutex
	engineStarted bool
)

var (
	scratchMu      sync.Mutex
	scratchDBs     []string
	cleanupOnce    sync.Once
	cleanupBackoff = 250 * time.Millisecond
)

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("소스 위치 확인 실패")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func defaultComposeUp() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d", "--wait")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compose up 실패: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func connectDSN(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, ResolveDSN())
}

func defaultDropDB(ctx context.Context, name string) error {
	conn, err := connectDSN(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name)
	return err
}

func defaultPurge(ctx context.Context) error {
	conn, err := connectDSN(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "SELECT dolt_purge_dropped_databases()")
	return err
}

func acquireEngineLock() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "testengine.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func releaseEngineLock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func EnsureEngine(ctx context.Context) error {
	engineMu.Lock()
	defer engineMu.Unlock()
	if engineStarted {
		return nil
	}
	lock, err := acquireEngineLock()
	if err != nil {
		return fmt.Errorf("엔진 잠금 획득 실패: %w", err)
	}
	defer releaseEngineLock(lock)
	if err := composeUpFn(); err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			return fmt.Errorf("전용 테스트 엔진 기동 실패(FLINT_REALDB_REQUIRED=1): %w", err)
		}
		fmt.Println("경고: 전용 테스트 엔진 기동 실패 — 실DB 테스트를 skip합니다")
		return err
	}
	engineStarted = true
	return nil
}

func RegisterScratch(name string) {
	scratchMu.Lock()
	defer scratchMu.Unlock()
	scratchDBs = append(scratchDBs, name)
}

func dropScratch(ctx context.Context, name string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(cleanupBackoff * time.Duration(attempt))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if err = dropDBFn(ctx, name); err == nil {
			return nil
		}
	}
	return err
}

func RunCleanup(ctx context.Context) {
	cleanupOnce.Do(func() {
		scratchMu.Lock()
		names := append([]string(nil), scratchDBs...)
		scratchMu.Unlock()
		var failed []string
		for _, name := range names {
			if err := dropScratch(ctx, name); err != nil {
				failed = append(failed, name)
			}
		}
		if len(failed) > 0 {
			fmt.Fprintf(os.Stderr, "정리 실패 DB: %s\n", strings.Join(failed, ", "))
		}
		if ResolveDSN() == TestDSNDefault() {
			if err := purgeFn(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "purge 실패: %v\n", err)
			}
		}
	})
}
