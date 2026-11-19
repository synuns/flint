// usage_retry_test.go — the 40001 (serialization failure) retry
// contract of the bumpDailyFlat UPSERT (fast unit tests — no real DB
// needed). CI-demonstrated: under a parallel 8-goroutine race, the
// deployed Doltgres returns 40001 (locally it accepts LWW — an
// environment assumption error).
package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// pg40001 — a mock SQLSTATE 40001 error.
func pg40001() error { return &pgconn.PgError{Code: "40001", Message: "conflict"} }

// TestBumpRetrySucceedsAfterTransient — 40001 twice then success on the
// 3rd → nil, 3 attempts total.
func TestBumpRetrySucceedsAfterTransient(t *testing.T) {
	attempts := 0
	err := retryBump40001(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return pg40001()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("일시 40001 후 성공: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("시도 %d회, want 3(초기+재시도 2)", attempts)
	}
}

// TestBumpRetryExhausts — 3 consecutive 40001s → an error returned
// (cause preserved), with no further attempts.
func TestBumpRetryExhausts(t *testing.T) {
	attempts := 0
	err := retryBump40001(context.Background(), func() error {
		attempts++
		return pg40001()
	})
	if err == nil {
		t.Fatal("3회 소진 시 에러여야 한다")
	}
	if attempts != 3 {
		t.Fatalf("시도 %d회, want 3(소진 상한)", attempts)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
		t.Fatalf("소진 에러가 40001 원인을 보존해야 한다: %v", err)
	}
}

// TestBumpRetryNon40001Immediate — a non-40001 error returns
// immediately without retry.
func TestBumpRetryNon40001Immediate(t *testing.T) {
	attempts := 0
	boom := errors.New("boom-nonconflict")
	err := retryBump40001(context.Background(), func() error {
		attempts++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("원인 그대로 반환: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("시도 %d회, want 1(비40001 즉시 반환)", attempts)
	}
}

// TestBumpRetryCtxCancel — a ctx cancellation during a retry wait
// returns ctx.Err().
func TestBumpRetryCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryBump40001(ctx, func() error {
		attempts++
		cancel()
		return pg40001()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("시도 %d회, want 1(취소 시 중단)", attempts)
	}
}
