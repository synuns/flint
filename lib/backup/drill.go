package backup

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
)

const (
	DrillDB = "flint_drill_db"

	LastDrillKey = "last_drill"

	drillTimeout = 120 * time.Second

	drillCleanupTimeout = 30 * time.Second
)

type DrillResult struct {
	Expected int
	Restored int
	Ok       bool
}

var (
	drillCreateSeqRe = regexp.MustCompile(`(?i)^CREATE\s+SEQUENCE\s+IF\s+NOT\s+EXISTS\s+public\.`)
	drillSetvalRe    = regexp.MustCompile(`(?i)^SELECT\s+setval\s*\(\s*'public\.`)
)

func skipForDrill(stmt string) bool {
	s := stripComments(stmt)
	return drillCreateSeqRe.MatchString(s) || drillSetvalRe.MatchString(s)
}

func countInserts(stmts []string, table string) int {
	re := regexp.MustCompile(`(?i)^INSERT\s+INTO\s+"?` + regexp.QuoteMeta(table) + `"?\s*\(`)
	n := 0
	for _, s := range stmts {
		if re.MatchString(stripComments(s)) {
			n++
		}
	}
	return n
}

func RunDrill(dsn string, dumpPath string) (res DrillResult, err error) {
	return RunDrillWithKey(dsn, dumpPath, "")
}

func RunDrillWithKey(dsn string, dumpPath string, keyPath string) (res DrillResult, err error) {

	data, err := ReadDumpBytes(dumpPath, keyPath)
	if err != nil {
		return DrillResult{}, err
	}
	return RunDrillData(dsn, data)
}

func RunDrillData(dsn string, data []byte) (res DrillResult, err error) {
	ctx, cancel := drillCtxFn()
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return DrillResult{}, err
	}
	defer pool.Close()

	drop := func(ctx context.Context) error {
		return db.Retry(ctx, func() error { return dropDrillDBFn(ctx, pool) }, drillDropBackoff)
	}
	if derr := drop(ctx); derr != nil {
		return DrillResult{}, fmt.Errorf("drill: pre-clean %s failed: %w", DrillDB, derr)
	}
	defer func() {

		cctx, ccancel := context.WithTimeout(context.Background(), drillCleanupTimeout)
		defer ccancel()
		if derr := drop(cctx); derr != nil && err == nil {
			err = fmt.Errorf("drill: %s drop retries exhausted (still present): %w", DrillDB, derr)
		}
	}()

	if _, err := pool.Exec(ctx, `CREATE DATABASE `+DrillDB); err != nil {
		return DrillResult{}, fmt.Errorf("drill: create database: %w", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: build drill DSN: %w", err)
	}
	cfg.ConnConfig.Database = DrillDB
	drillPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: connect drill DB: %w", err)
	}

	var expected, restored int
	replayErr := func() error {
		defer drillPool.Close()
		conn, err := drillPool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("drill: replay connection: %w", err)
		}
		defer conn.Release()
		stmts := splitStatements(string(data))
		expected = countInserts(stmts, "knowledge")
		for _, stmt := range stmts {
			if stripComments(stmt) == "" || skipForDrill(stmt) {
				continue
			}
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("drill: %v\n  in: %.120s", err, stmt)
			}
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM knowledge`).Scan(&restored); err != nil {
			return fmt.Errorf("drill: knowledge row count: %w", err)
		}
		return nil
	}()
	if replayErr != nil {
		return DrillResult{Expected: expected}, replayErr
	}
	res = DrillResult{Expected: expected, Restored: restored, Ok: expected == restored}
	if !res.Ok {
		return res, nil
	}

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		return res, fmt.Errorf("drill: ensure maintenance_runs: %w", err)
	}
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.maintenance_runs (kind, details) VALUES ('drill', $1)`,
		fmt.Sprintf("expected=%d restored=%d", expected, restored)); err != nil {
		return res, fmt.Errorf("drill: maintenance_runs: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		LastDrillKey, now.Format(time.RFC3339)); err != nil {
		return res, fmt.Errorf("drill: meta last_drill: %w", err)
	}
	return res, nil
}

var drillDropBackoff = []time.Duration{0, time.Second, 3 * time.Second}

var dropDrillDBFn = dropDrillDB

var drillCtxFn = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), drillTimeout)
}

var drillResidueFn = drillDBResidue

func dropDrillDB(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+DrillDB); err != nil {
		return err
	}
	n, err := drillResidueFn(ctx, pool)
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("drill: %s still present — manual DROP required", DrillDB)
	}
	return nil
}

func drillDBResidue(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
