// Package db 는 Doltgres 접속 풀과 재시도 정책을 제공한다.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/store"
)

// 에러 심볼 단일 소유는 lib/store — db는 별칭만 제공한다.
var (
	ErrServerDown       = store.ErrServerDown
	ErrRetriesExhausted = store.ErrRetriesExhausted
)

// DefaultDelays 는 기본 재시도 간격: 즉시, 1초, 3초.
var DefaultDelays = []time.Duration{0, 1 * time.Second, 3 * time.Second}

// Connect 는 DSN으로 풀을 만들고 Ping으로 생존을 확인한다.
// 서버에 닿을 수 없으면 ErrServerDown을 돌려준다(재시도 없음).
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
	}
	return pool, nil
}

// Retry 는 fn을 delays 횟수만큼 시도한다. delays[i]는 i+1번째 시도 전 대기다.
// fn이 nil을 돌려주면 즉시 nil, ErrServerDown이면 재시도 없이 즉시 반환,
// 모두 실패하면 ErrRetriesExhausted. 재시도 중 ctx 취소는 ctx.Err() 반환.
func Retry(ctx context.Context, fn func() error, delays []time.Duration) error {
	for i, d := range delays {
		if i > 0 || d > 0 {
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrServerDown) {
			return err
		}
	}
	return ErrRetriesExhausted
}

// sleepCtx 는 ctx 취소 시 즉시 반환되는 sleep이다.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
