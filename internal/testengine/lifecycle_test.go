package testengine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func resetLifecycle(t *testing.T) {
	t.Helper()
	unset(t, "FLINT_REALDB_REQUIRED")
	engineMu.Lock()
	engineStarted = false
	engineMu.Unlock()
	scratchMu.Lock()
	scratchDBs = nil
	scratchMu.Unlock()
	cleanupOnce = sync.Once{}
	cleanupBackoff = time.Millisecond
}

func fakeSeams(t *testing.T, up func() error, drop func(ctx context.Context, name string) error, purge func(ctx context.Context) error) {
	t.Helper()
	if up != nil {
		orig := composeUpFn
		composeUpFn = up
		t.Cleanup(func() { composeUpFn = orig })
	}
	if drop != nil {
		orig := dropDBFn
		dropDBFn = drop
		t.Cleanup(func() { dropDBFn = orig })
	}
	if purge != nil {
		orig := purgeFn
		purgeFn = purge
		t.Cleanup(func() { purgeFn = orig })
	}
}

func captureOutput(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	orig := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("파이프: %v", err)
	}
	*target = w
	fn()
	w.Close()
	*target = orig
	data, _ := io.ReadAll(r)
	return string(data)
}

func TestEnsureEngineConcurrentSingleComposeUp(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	var calls int32
	fakeSeams(t, func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, nil, nil)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = EnsureEngine(context.Background())
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("동시 8고루틴 기동 시도 %d회, want 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("고루틴 %d 오류: %v", i, err)
		}
	}
}

func TestEnsureEngineAlreadyStartedSkipsCompose(t *testing.T) {
	resetLifecycle(t)
	var calls int32
	fakeSeams(t, func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, nil, nil)
	if err := EnsureEngine(context.Background()); err != nil {
		t.Fatalf("첫 기동: %v", err)
	}
	if err := EnsureEngine(context.Background()); err != nil {
		t.Fatalf("재호출: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("기동 시도 %d회, want 1(lazy 재판단 무)", got)
	}
}

func TestEnsureEngineFailureLocalWarnsAndErrors(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	fakeSeams(t, func() error { return fmt.Errorf("compose 부재") }, nil, nil)
	out := captureOutput(t, &os.Stdout, func() {
		if err := EnsureEngine(context.Background()); err == nil {
			t.Fatalf("로컬 기동 실패 시 error 와야 함")
		}
	})
	if !strings.Contains(out, "skip") {
		t.Fatalf("경고 1행 부재: %q", out)
	}
}

func TestEnsureEngineFailureCIRequiredNoWarning(t *testing.T) {
	resetLifecycle(t)
	t.Setenv("FLINT_REALDB_REQUIRED", "1")
	fakeSeams(t, func() error { return fmt.Errorf("compose 부재") }, nil, nil)
	out := captureOutput(t, &os.Stdout, func() {
		if err := EnsureEngine(context.Background()); err == nil {
			t.Fatalf("CI 기동 실패 시 error 와야 함")
		}
	})
	if strings.Contains(out, "skip") {
		t.Fatalf("CI 필수 모드에 경고 출력 불필요: %q", out)
	}
}

func TestRunCleanupDropsRegisteredAndPurgesOnce(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	var dropped []string
	var purges int32
	fakeSeams(t, nil,
		func(ctx context.Context, name string) error {
			dropped = append(dropped, name)
			return nil
		},
		func(ctx context.Context) error {
			atomic.AddInt32(&purges, 1)
			return nil
		})
	RegisterScratch("flint_t3_a")
	RegisterScratch("flint_t3_b")
	RunCleanup(context.Background())
	if fmt.Sprint(dropped) != "[flint_t3_a flint_t3_b]" {
		t.Fatalf("DROP 순서 부정합: %v", dropped)
	}
	if got := atomic.LoadInt32(&purges); got != 1 {
		t.Fatalf("purge %d회, want 1", got)
	}
	RunCleanup(context.Background())
	if got := atomic.LoadInt32(&purges); got != 1 {
		t.Fatalf("재호출 purge %d회, want 1(런당 1회)", got)
	}
	if len(dropped) != 2 {
		t.Fatalf("재호출 재DROP: %v", dropped)
	}
}

func TestRunCleanupPurgeOnlyForDefaultDSN(t *testing.T) {
	resetLifecycle(t)
	t.Setenv("FLINT_TEST_DSN", "postgresql://flint:pw@127.0.0.1:5499/other")
	var purges int32
	var drops int32
	fakeSeams(t, nil,
		func(ctx context.Context, name string) error {
			atomic.AddInt32(&drops, 1)
			return nil
		},
		func(ctx context.Context) error {
			atomic.AddInt32(&purges, 1)
			return nil
		})
	RegisterScratch("flint_t3_c")
	RunCleanup(context.Background())
	if got := atomic.LoadInt32(&drops); got != 1 {
		t.Fatalf("오버라이드 DSN에서도 DROP %d회, want 1", got)
	}
	if got := atomic.LoadInt32(&purges); got != 0 {
		t.Fatalf("오버라이드 DSN에 purge %d회, want 0", got)
	}
}

func TestRunCleanupRetryBackoffRecovers(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	attempts := map[string]int{}
	fakeSeams(t, nil,
		func(ctx context.Context, name string) error {
			attempts[name]++
			if attempts[name] < 3 {
				return fmt.Errorf("일시 오류")
			}
			return nil
		}, nil)
	RegisterScratch("flint_t3_d")
	stderr := captureOutput(t, &os.Stderr, func() {
		RunCleanup(context.Background())
	})
	if attempts["flint_t3_d"] != 3 {
		t.Fatalf("재시도 후 성공 실험: 시도 %d회, want 3", attempts["flint_t3_d"])
	}
	if strings.Contains(stderr, "flint_t3_d") {
		t.Fatalf("성공한 DB가 실패 보고됨: %q", stderr)
	}
}

func TestRunCleanupFailureReportedPerName(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	fakeSeams(t, nil,
		func(ctx context.Context, name string) error {
			return fmt.Errorf("영구 오류")
		}, nil)
	RegisterScratch("flint_t3_e")
	stderr := captureOutput(t, &os.Stderr, func() {
		RunCleanup(context.Background())
	})
	if !strings.Contains(stderr, "flint_t3_e") {
		t.Fatalf("실패 이름 1행 보고 부재: %q", stderr)
	}
}

func TestLifecycleRealSlow(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("SUPERLITE_REAL=1 required")
	}
	resetLifecycle(t)
	ctx := context.Background()
	if err := EnsureEngine(ctx); err != nil {
		t.Skipf("엔진 기동 불가: %v", err)
	}
	name := fmt.Sprintf("testengine_t3_%d", time.Now().UnixNano()%1e9)
	base, err := pgx.Connect(ctx, ResolveDSN())
	if err != nil {
		t.Skipf("실DB 접속 불가: %v", err)
	}
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	base.Close(ctx)
	RegisterScratch(name)
	RunCleanup(ctx)
	u, err := url.Parse(ResolveDSN())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	scratch, err := pgx.Connect(ctx, u.String())
	if err == nil {
		scratch.Close(ctx)
		t.Fatalf("정리 후 스크래치 DB 접속됨: %s", name)
	}
}
