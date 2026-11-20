// drill.go — restore --drill: backup recovery practice (a restore
// rehearsal). Replays the dump SQL into the temporary database
// flint_drill_db, compares knowledge row counts, and always DROPs the
// database on exit (including on failure). The original public data is
// untouchable.
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
	// DrillDB — the temporary database dedicated to drill replay.
	DrillDB = "flint_drill_db"
	// LastDrillKey — the meta key for the last successful drill time (RFC3339). Read by maintain --check.
	LastDrillKey = "last_drill"
	// drillTimeout — the per-drill (replay) time limit.
	drillTimeout = 120 * time.Second
	// drillCleanupTimeout — a separate limit for the final-cleanup DROP.
	// Split from the replay ctx (120s) — even if replay exhausts its whole
	// budget, cleanup still runs on a live ctx.
	drillCleanupTimeout = 30 * time.Second
)

// DrillResult — the drill outcome. Expected is the number of knowledge
// INSERT statements in the dump; Restored is the knowledge row count
// actually replayed into flint_drill_db. Ok means the two match.
type DrillResult struct {
	Expected int
	Restored int
	Ok       bool
}

// public sequence-manipulation statements emitted by dump — skipped during
// drill replay. Replaying Dump's CREATE SEQUENCE IF NOT EXISTS public.x or
// SELECT setval('public.x',…) as-is would alter the target DB's public
// sequences. The drill DB is discarded wholesale so this is effectively
// harmless, but it is also unnecessary for the row-count comparison, so it
// is skipped.
var (
	drillCreateSeqRe = regexp.MustCompile(`(?i)^CREATE\s+SEQUENCE\s+IF\s+NOT\s+EXISTS\s+public\.`)
	drillSetvalRe    = regexp.MustCompile(`(?i)^SELECT\s+setval\s*\(\s*'public\.`)
)

// skipForDrill — decides which statements to exclude from drill replay (public sequence manipulation).
func skipForDrill(stmt string) bool {
	s := stripComments(stmt)
	return drillCreateSeqRe.MatchString(s) || drillSetvalRe.MatchString(s)
}

// countInserts — counts INSERT INTO table statements in a statement list (case- and quote-insensitive).
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

// RunDrill — replays the dumpPath dump into the temporary database
// flint_drill_db and compares knowledge row counts. Replay happens on a
// dedicated drill-DB pool separate from the original DSN, so no connection
// state in the original pool (search_path, etc.) is contaminated. On
// success it records one maintenance_runs row (kind=drill) and the meta
// last_drill on the original DSN. The drill DB is always DROPped regardless
// of success or failure (40001 recovers via retry). A row-count mismatch is
// reported as Ok=false, not an error. When DROP retries are exhausted it
// does not hide the residue and returns an explicit failure instead of
// success (contract: no residue even on replay failure — CI run
// 39719705178). Even when DROP returns nil, residue is verified via
// pg_database (defense against silent no-op engine defects). Regardless of
// the doltgresql defect where per-table/per-schema DROPs silently no-op and
// leave residue (observed on 1.3.3 and 1.4.0), database-level deletion
// works even under this combination of defects (verified). The final-cleanup
// DROP runs on a short timeout independent of the replay ctx (even if
// replay exhausts 120s, cleanup always runs).
func RunDrill(dsn string, dumpPath string) (res DrillResult, err error) {
	return RunDrillWithKey(dsn, dumpPath, "")
}

// RunDrillWithKey — drill with encrypted-dump support (keyPath = the age
// identity path; blank means plaintext only). Reads the file, then defers
// to RunDrillData.
func RunDrillWithKey(dsn string, dumpPath string, keyPath string) (res DrillResult, err error) {
	// The single read path — age decryption and masked-dump rejection apply here.
	data, err := ReadDumpBytes(dumpPath, keyPath)
	if err != nil {
		return DrillResult{}, err
	}
	return RunDrillData(dsn, data)
}

// RunDrillData — a replay drill on pre-read (verified/decrypted) bytes
// (the CLI replays the exact bytes it verified — no TOCTOU window).
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
	if derr := drop(ctx); derr != nil { // pre-clean leftover residue (in case of a previous abnormal exit)
		return DrillResult{}, fmt.Errorf("drill: pre-clean %s failed: %w", DrillDB, derr)
	}
	defer func() {
		// The final-cleanup DROP uses its own timeout, independent of the
		// replay ctx — even if replay exhausts its whole budget, cleanup
		// always runs on a live ctx.
		cctx, ccancel := context.WithTimeout(context.Background(), drillCleanupTimeout)
		defer ccancel()
		if derr := drop(cctx); derr != nil && err == nil {
			err = fmt.Errorf("drill: %s drop retries exhausted (still present): %w", DrillDB, derr)
		}
	}()

	if _, err := pool.Exec(ctx, `CREATE DATABASE `+DrillDB); err != nil {
		return DrillResult{}, fmt.Errorf("drill: create database: %w", err)
	}

	// Drill DB connection — inherits the original DSN settings (credentials,
	// host) and changes only the target database. Replay happens exclusively
	// on this separate pool.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: build drill DSN: %w", err)
	}
	cfg.ConnConfig.Database = DrillDB
	drillPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: connect drill DB: %w", err)
	}

	// Replay — the dump contains BEGIN/COMMIT, so transaction state lingers
	// on the connection: run everything on a single connection. Connections
	// and the pool are released/closed on every path — DROP DATABASE only
	// succeeds once the drill DB connections are closed.
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

	// Success record — written only where explicitly declared, on the
	// original DSN's public schema. A dev DB created before v0.4 may lack
	// maintenance_runs (a migration-gate gap) — ensure the table so a drill
	// success is not buried by a recording failure.
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

// drillDropBackoff — retry intervals for 40001 (serialization failure) on
// the drill DB DROP. Under CI serialization load, DROP once failed by
// colliding with another session's commit, and ignoring the error left the
// DB behind (CI run 39719705178) — retries briefly 3 times, the same
// recovery pattern as promotion commits and view reapplication.
var drillDropBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// dropDrillDBFn — DROP injection seam (regression tests fake transient failures).
var dropDrillDBFn = dropDrillDB

// drillCtxFn — seam for building the replay context (unit tests inject expiry).
var drillCtxFn = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), drillTimeout)
}

// drillResidueFn — seam for the pg_database residue probe (unit tests inject residue).
var drillResidueFn = drillDBResidue

// dropDrillDB — removes the flint_drill_db database and verifies no
// residue. Deletes at the database level instead of per-table/per-schema
// DROPs — doltgresql's silent DROP no-op defect cannot spread into
// replay-table residue (verified). Even a nil return from DROP is not
// trusted: a no-op engine defect leaves residue without an error. It checks
// pg_database directly and returns an explicit failure instead of hiding
// residue.
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

// drillDBResidue — the flint_drill_db row count in pg_database (0 = absent).
func drillDBResidue(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
