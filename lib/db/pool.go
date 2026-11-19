// Package db provides the Doltgres connection pool and retry policy.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/store"
)

// Single ownership of the error symbols lives in lib/store — db provides
// aliases only.
var (
	ErrServerDown       = store.ErrServerDown
	ErrRetriesExhausted = store.ErrRetriesExhausted
)

// DefaultDelays is the default retry spacing: immediate, 1s, 3s.
var DefaultDelays = []time.Duration{0, 1 * time.Second, 3 * time.Second}

// DefaultConnectTimeout — the 5s connection-attempt cap. Fails remote
// unreachability early (packet drop — unlike a refused connection, it
// hangs until the kernel timeout) so the session hook finishes with
// exit 2 within its budget (10s).
const DefaultConnectTimeout = 5 * time.Second

// PoolConfig — parses the DSN and returns a pool config with a
// guaranteed ConnectTimeout. Unspecified (0) defaults to 5s; a longer
// user-specified value (unreachable-delay) is capped at 5s; a shorter
// one is kept. A parse failure is wrapped as ErrServerDown.
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

// Connect builds a pool from the DSN and confirms liveness with Ping.
// Returns ErrServerDown when the server is unreachable (no retry).
// Connection attempts follow PoolConfig's ConnectTimeout (default 5s
// cap).
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

// Retry attempts fn delays-many times. delays[i] is the wait before the
// i+1-th attempt. fn returning nil returns nil immediately;
// ErrServerDown returns immediately without retry; exhausting all
// attempts yields ErrRetriesExhausted. A ctx cancellation during retry
// returns ctx.Err().
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
	// Wraps the last error too — returning only the exhaustion buries
	// the cause and makes diagnosis impossible (a diagnosability defect
	// surfaced during a CI investigation).
	if last != nil {
		return fmt.Errorf("%w: %v", ErrRetriesExhausted, last)
	}
	return ErrRetriesExhausted
}

// sleepCtx is a sleep that returns immediately on ctx cancellation.
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
