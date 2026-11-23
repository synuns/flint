package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/store"
)

var (
	ErrServerDown       = store.ErrServerDown
	ErrRetriesExhausted = store.ErrRetriesExhausted
)

var DefaultDelays = []time.Duration{0, 1 * time.Second, 3 * time.Second}

const DefaultConnectTimeout = 5 * time.Second

func PoolConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
	}
	if cfg.ConnConfig.ConnectTimeout <= 0 || cfg.ConnConfig.ConnectTimeout > DefaultConnectTimeout {
		cfg.ConnConfig.ConnectTimeout = DefaultConnectTimeout
	}
	return cfg, nil
}

func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := PoolConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%w: %v", ErrServerDown, err)
	}
	return pool, nil
}

func Retry(ctx context.Context, fn func() error, delays []time.Duration) error {
	var last error
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
		last = err
	}

	if last != nil {
		return fmt.Errorf("%w: %v", ErrRetriesExhausted, last)
	}
	return ErrRetriesExhausted
}

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
