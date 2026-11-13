// usage_retry_test.go — v1.2.3 T9 후속2: bumpDailyFlat UPSERT의 40001
// (직렬화 실패) 재시도 계약(빠른 단위 테스트 — 실DB 불필요). CI 실증:
// 병렬 8고루틴 경합에서 배포 Doltgres가 40001을 반환한다(로컬은 LWW
// 수용 — 환경 가정 오류).
package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// pg40001 — SQLSTATE 40001 흉내 오류.
func pg40001() error { return &pgconn.PgError{Code: "40001", Message: "conflict"} }

// TestBumpRetrySucceedsAfterTransient — 2회 40001 후 3회째 성공 → nil,
// 시도 총 3회.
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

// TestBumpRetryExhausts — 3회 연속 40001 → 에러 반환(원인 보존),
// 그 이상 시도하지 않는다.
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

// TestBumpRetryNon40001Immediate — 40001 아닌 에러는 재시도 없이 즉시 반환.
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

// TestBumpRetryCtxCancel — 재시도 대기 중 ctx 취소는 ctx.Err() 반환.
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
