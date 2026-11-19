package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryExhausts(t *testing.T) {
	attempts := 0
	fn := func() error {
		attempts++
		return errors.New("boom")
	}
	err := Retry(context.Background(), fn, DefaultDelays)
	if !errors.Is(err, ErrRetriesExhausted) {
		t.Fatalf("want ErrRetriesExhausted, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("want 3 attempts, got %d", attempts)
	}
}

func TestRetrySecondTry(t *testing.T) {
	attempts := 0
	fn := func() error {
		attempts++
		if attempts >= 2 {
			return nil
		}
		return errors.New("flaky")
	}
	err := Retry(context.Background(), fn, []time.Duration{0, 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("want 2 attempts, got %d", attempts)
	}
}

func TestRetryCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	fn := func() error {
		attempts++
		cancel()
		return errors.New("boom")
	}
	err := Retry(ctx, fn, []time.Duration{0, 10 * time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("want 1 attempt, got %d", attempts)
	}
}

func TestRetryServerDownImmediate(t *testing.T) {
	attempts := 0
	fn := func() error {
		attempts++
		return ErrServerDown
	}
	err := Retry(context.Background(), fn, []time.Duration{0, 10 * time.Millisecond})
	if !errors.Is(err, ErrServerDown) {
		t.Fatalf("want ErrServerDown, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("want 1 attempt, got %d", attempts)
	}
}

func TestConnectServerDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Port 5439 is unused — inducing a server-down
	_, err := Connect(ctx, "postgresql://flint:flint-dev@localhost:5439/flint")
	if !errors.Is(err, ErrServerDown) {
		t.Fatalf("want ErrServerDown, got %v", err)
	}
}

func TestConnectReal(t *testing.T) {
	// Without FLINT_DSN, skip instead of the 5433 fallback — see
	// testutil_test.go.
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect real doltgres: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestPoolConfigConnectTimeout — the pool config's connection timeout
// defaults to 5s (failing remote unreachability early — exit 2 within
// the hooks' 10s budget). A shorter DSN-specified value is kept; a
// longer one is capped at 5s.
func TestPoolConfigConnectTimeout(t *testing.T) {
	cfg, err := PoolConfig("postgresql://flint:flint-dev@localhost:5434/flint")
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	if got := cfg.ConnConfig.ConnectTimeout; got != 5*time.Second {
		t.Fatalf("ConnectTimeout = %v, want 5s", got)
	}
	cfg, err = PoolConfig("postgresql://flint:flint-dev@localhost:5434/flint?connect_timeout=1")
	if err != nil {
		t.Fatalf("PoolConfig(shorter): %v", err)
	}
	if got := cfg.ConnConfig.ConnectTimeout; got != 1*time.Second {
		t.Fatalf("사용자 단축값 유지 실패: %v, want 1s", got)
	}
	cfg, err = PoolConfig("postgresql://flint:flint-dev@localhost:5434/flint?connect_timeout=30")
	if err != nil {
		t.Fatalf("PoolConfig(longer): %v", err)
	}
	if got := cfg.ConnConfig.ConnectTimeout; got != 5*time.Second {
		t.Fatalf("장값 상한 실패: %v, want 5s", got)
	}
}
