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
	// 포트 5439는 미사용 — 서버 다운 유도
	_, err := Connect(ctx, "postgresql://flint:flint-dev@localhost:5439/flint")
	if !errors.Is(err, ErrServerDown) {
		t.Fatalf("want ErrServerDown, got %v", err)
	}
}

func TestConnectReal(t *testing.T) {
	// FLINT_DSN 미설정 시 5433 폴백 대신 스킵 — testutil_test.go 참조.
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
