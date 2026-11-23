package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/sqlfs"
	"github.com/synuns/flint/lib/store"
)

var (
	schemaSQLFn = sqlfs.SchemaSQL
	viewsSQLFn  = sqlfs.ViewsSQL
)

var reapplyViewsFn = (*Repo).reapplyViews

var kindTables = map[string]string{
	"knowledge": "knowledge",
	"session":   "sessions",
	"project":   "projects",
}

type Repo struct {
	pool *pgxpool.Pool
	dsn  string
}

func NewRepo(ctx context.Context, dsn string) (*Repo, error) {
	p, err := Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Repo{pool: p, dsn: dsn}, nil
}

func (r *Repo) Close() { r.pool.Close() }

func (r *Repo) Migrate(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("migrate: check schema_version: %w", err)
	}

	tablesCurrent := false
	if err == nil {
		if n, perr := strconv.Atoi(version); perr == nil && n >= 1 {
			tablesCurrent = true
		}
	}
	if !tablesCurrent {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate: begin: %w", err)
		}
		defer tx.Rollback(ctx)
		if err := execSQLScript(ctx, tx, schemaSQLFn()); err != nil {
			return fmt.Errorf("migrate: schema: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ('schema_version', '1')
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
			return fmt.Errorf("migrate: meta: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: commit: %w", err)
		}
	}

	viewsSQL := viewsSQLFn()
	return Retry(ctx, func() error {
		if r.viewsCurrent(ctx, viewsSQL) {
			return nil
		}
		if err := reapplyViewsFn(r, ctx); err != nil {
			return err
		}
		sig, err := r.viewsSig(ctx, viewsSQL)
		if err != nil {
			return err
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ('views_hash', $1), ('views_sig', $2)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			hashSQL(viewsSQL), sig); err != nil {
			return fmt.Errorf("views: record hash/sig: %w", err)
		}
		return nil
	}, []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 9 * time.Second})
}

func (r *Repo) viewsCurrent(ctx context.Context, viewsSQL string) bool {
	var storedHash, storedSig string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'views_hash'`).Scan(&storedHash); err != nil {
		return false
	}
	if storedHash != hashSQL(viewsSQL) {
		return false
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'views_sig'`).Scan(&storedSig); err != nil {
		return false
	}
	sig, err := r.viewsSig(ctx, viewsSQL)
	if err != nil {
		return false
	}
	return sig == storedSig
}

func (r *Repo) viewsSig(ctx context.Context, viewsSQL string) (string, error) {
	names := viewNames(viewsSQL)
	if len(names) == 0 {
		return "", fmt.Errorf("views: view name not found in SQL")
	}
	var b strings.Builder
	for _, name := range names {
		rows, err := r.pool.Query(ctx, "SELECT * FROM "+name+" LIMIT 0")
		if err != nil {
			return "", fmt.Errorf("views: query %s: %w", name, err)
		}
		var cols []string
		for _, fd := range rows.FieldDescriptions() {
			cols = append(cols, string(fd.Name))
		}
		rows.Close()
		fmt.Fprintf(&b, "%s:%s\n", name, strings.Join(cols, ","))
	}
	return b.String(), nil
}

var viewNameRe = regexp.MustCompile(`(?is)CREATE\s+OR\s+REPLACE\s+VIEW\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

func viewNames(script string) []string {
	matches := viewNameRe.FindAllStringSubmatch(script, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func hashSQL(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

func (r *Repo) reapplyViews(ctx context.Context) error {
	sep := "?"
	if strings.Contains(r.dsn, "?") {
		sep = "&"
	}
	conn, err := pgx.Connect(ctx, r.dsn+sep+"default_query_exec_mode=simple_protocol")
	if err != nil {
		return fmt.Errorf("views: simple connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx,
		`DROP VIEW IF EXISTS active_knowledge, hot_view, supersede_chains`); err != nil {
		return fmt.Errorf("views: drop: %w", err)
	}
	for _, stmt := range splitStatements(viewsSQLFn()) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("views: create: %w", err)
		}
	}
	return nil
}

var batchStmtChunk = 1000

type insertStmt struct {
	sql  string
	args []any
}

func buildInsertStmt(rec map[string]any) (insertStmt, error) {
	kind, _ := rec["kind"].(string)
	table, ok := kindTables[kind]
	if !ok {
		return insertStmt{}, fmt.Errorf("insert: unknown kind %q", kind)
	}
	cols := make([]string, 0, len(rec))
	for k := range rec {
		if k == "kind" {
			continue
		}
		cols = append(cols, k)
	}
	sort.Strings(cols)
	args := make([]any, len(cols))
	ph := make([]string, len(cols))
	qc := make([]string, len(cols))
	for i, c := range cols {
		args[i] = rec[c]
		ph[i] = fmt.Sprintf("$%d", i+1)
		qc[i] = quoteIdent(c)
	}
	return insertStmt{
		sql: fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id",
			quoteIdent(table), strings.Join(qc, ", "), strings.Join(ph, ", ")),
		args: args,
	}, nil
}

func (r *Repo) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	stmts := make([]insertStmt, 0, len(recs))
	for _, rec := range recs {
		s, err := buildInsertStmt(rec)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, mapInsertErr(err)
	}
	defer tx.Rollback(ctx)
	ids := make([]int, 0, len(stmts))
	for start := 0; start < len(stmts); start += batchStmtChunk {
		end := min(start+batchStmtChunk, len(stmts))
		b := &pgx.Batch{}
		for _, s := range stmts[start:end] {
			b.Queue(s.sql, s.args...)
		}
		res := tx.SendBatch(ctx, b)
		for i := start; i < end; i++ {
			var id int
			if err := res.QueryRow().Scan(&id); err != nil {
				res.Close()
				return nil, mapInsertErr(err)
			}
			ids = append(ids, id)
		}
		if err := res.Close(); err != nil {
			return nil, mapInsertErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapInsertErr(err)
	}
	return ids, nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func (r *Repo) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	id, _, _, exists, err := r.DupRowByHash(ctx, hash)
	return id, exists, err
}

func (r *Repo) DupRowByHash(ctx context.Context, hash string) (id int, topic, state string, exists bool, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT id, topic, status FROM knowledge WHERE content_hash = $1`, hash).
		Scan(&id, &topic, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", "", false, fmt.Errorf("dup_row_by_hash: %w", err)
	}
	return id, topic, state, true, nil
}

func (r *Repo) ExistsHash(ctx context.Context, hash string) (bool, error) {
	_, dup, err := r.DupIDByHash(ctx, hash)
	return dup, err
}

func (r *Repo) ExistsProject(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE name = $1)`, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("exists_project: %w", err)
	}
	return exists, nil
}

var transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
	ct, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (r *Repo) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := transitionExecFn(ctx, r, `
		UPDATE knowledge SET status = 'invalidated', invalid_at = now()
		WHERE id = ANY($1) AND invalid_at IS NULL`, ids)
	if err != nil {
		return 0, fmt.Errorf("invalidate(%s): %w", reason, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: invalidate(%s): 0 rows updated (race)", ErrNotFound, reason)
	}
	return n, nil
}

const SupersedeCycleDepthLimit = 64

const SupersedeCycleSQL = `
	WITH RECURSIVE lineage AS (
		SELECT id, superseded_by, 1 AS depth
		FROM knowledge WHERE id = $2
		UNION ALL
		SELECT k.id, k.superseded_by, l.depth + 1
		FROM knowledge k
		JOIN lineage l ON k.id = l.superseded_by
		WHERE l.depth < $3
	)
	SELECT EXISTS (SELECT 1 FROM lineage WHERE id = $1),
	       EXISTS (SELECT 1 FROM lineage WHERE depth >= $3)`

func (r *Repo) WouldCreateCycle(ctx context.Context, oldID, newID int) (bool, error) {
	return wouldCreateCycle(ctx, r.pool.QueryRow, oldID, newID)
}

func wouldCreateCycle(ctx context.Context, row supersedeQueryFn, oldID, newID int) (bool, error) {
	var cyc, truncated bool
	if err := row(ctx, SupersedeCycleSQL,
		oldID, newID, SupersedeCycleDepthLimit).Scan(&cyc, &truncated); err != nil {
		return false, fmt.Errorf("supersede_cycle: %w", err)
	}
	if cyc {
		return true, nil
	}
	if truncated {
		return false, fmt.Errorf(
			"supersede_cycle: lineage walk hit the depth cap (%d) — refusing conservatively",
			SupersedeCycleDepthLimit)
	}
	return false, nil
}

const supersedeLockKey = 0x666c696e74537570

type supersedeExecFn = func(ctx context.Context, query string, args ...any) (int64, error)

type supersedeQueryFn = func(ctx context.Context, query string, args ...any) pgx.Row

var supersedeLockFn = func(ctx context.Context, r *Repo) (supersedeExecFn, supersedeQueryFn, func(), error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("supersede: lock: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(
		`SELECT pg_advisory_lock(%d)`, supersedeLockKey)); err != nil {
		conn.Release()
		return nil, nil, nil, fmt.Errorf("supersede: lock: %w", err)
	}
	exec := func(ctx context.Context, query string, args ...any) (int64, error) {
		ct, err := conn.Exec(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return ct.RowsAffected(), nil
	}
	query := func(ctx context.Context, q string, args ...any) pgx.Row {
		return conn.QueryRow(ctx, q, args...)
	}
	released := false
	unlock := func() {
		if released {
			return
		}
		released = true

		_, _ = conn.Exec(context.Background(), fmt.Sprintf(
			`SELECT pg_advisory_unlock(%d)`, supersedeLockKey))
		conn.Release()
	}
	return exec, query, unlock, nil
}

const supersedeGuardedSQL = `
		UPDATE knowledge
		SET status = 'invalidated',
		    superseded_by = $2,
		    invalid_at = $3
		WHERE id = $1 AND status = 'active' AND invalid_at IS NULL
		AND NOT EXISTS (
			WITH RECURSIVE lineage AS (
				SELECT id, superseded_by, 1 AS depth
				FROM knowledge WHERE id = $2
				UNION ALL
				SELECT k.id, k.superseded_by, l.depth + 1
				FROM knowledge k
				JOIN lineage l ON k.id = l.superseded_by
				WHERE l.depth < $4
			)
			SELECT 1 FROM lineage WHERE id = $1 OR depth >= $4)`

func (r *Repo) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	exec, _, unlock, err := supersedeLockFn(ctx, r)
	if err != nil {
		return 0, err
	}
	defer unlock()
	n, err := exec(ctx, supersedeGuardedSQL,
		oldID, newID, at, SupersedeCycleDepthLimit)
	if err != nil {
		return 0, fmt.Errorf("supersede: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: supersede: id=%d update not applied (race — retry)", ErrNotFound, oldID)
	}
	return n, nil
}

const supersededRefGuardedSQL = `
		UPDATE knowledge
		SET superseded_by = $2
		WHERE id = $1 AND superseded_by IS NULL
		AND NOT EXISTS (
			WITH RECURSIVE lineage AS (
				SELECT id, superseded_by, 1 AS depth
				FROM knowledge WHERE id = $2
				UNION ALL
				SELECT k.id, k.superseded_by, l.depth + 1
				FROM knowledge k
				JOIN lineage l ON k.id = l.superseded_by
				WHERE l.depth < $3
			)
			SELECT 1 FROM lineage WHERE id = $1 OR depth >= $3)`

func (r *Repo) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	exec, query, unlock, err := supersedeLockFn(ctx, r)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if cyc, err := wouldCreateCycle(ctx, query, oldID, newID); err != nil {
		return 0, fmt.Errorf("superseded_ref: %w", err)
	} else if cyc {
		return 0, fmt.Errorf("superseded_ref: id=%d -> id=%d would create a supersede cycle", oldID, newID)
	}
	n, err := exec(ctx, supersededRefGuardedSQL,
		oldID, newID, SupersedeCycleDepthLimit)
	if err != nil {
		return 0, fmt.Errorf("superseded_ref: %w", err)
	}
	return n, nil
}

func (r *Repo) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("prune_invalidated: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM recall_events WHERE knowledge_id IN
		 (SELECT id FROM knowledge WHERE status = 'invalidated' AND invalid_at < $1)`,
		`DELETE FROM knowledge_reads WHERE knowledge_id IN
		 (SELECT id FROM knowledge WHERE status = 'invalidated' AND invalid_at < $1)`,
	} {
		if _, err := tx.Exec(ctx, q, before); err != nil {
			return 0, fmt.Errorf("prune_invalidated: orphans: %w", err)
		}
	}
	ct, err := tx.Exec(ctx,
		`DELETE FROM knowledge WHERE status = 'invalidated' AND invalid_at < $1`,
		before)
	if err != nil {
		return 0, fmt.Errorf("prune_invalidated: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("prune_invalidated: commit: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (r *Repo) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	n, err := transitionExecFn(ctx, r,
		`DELETE FROM meta WHERE key = ANY($1)`, keys)
	if err != nil {
		return 0, fmt.Errorf("meta_delete: %w", err)
	}
	return n, nil
}

func (r *Repo) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	n, err := transitionExecFn(ctx, r,
		`UPDATE sessions SET summary = $1, tasks = $2 WHERE id = $3`,
		summary, tasks, id)
	if err != nil {
		return 0, fmt.Errorf("update_session_summary: %w", err)
	}
	return n, nil
}

var auditStageTables = []string{"knowledge", "projects", "sessions", "meta", "maintenance_runs", "recall_events", "knowledge_links", "vault_files", "tuning_predictions", "echo_candidates"}

var telemetryStageTables = []string{"read_events", "hot_events", "knowledge_reads", "session_starts", "threshold_snapshots", "weekly_rollups"}

var telemetryStageSeqs = []string{"read_events_id_seq", "hot_events_id_seq", "knowledge_reads_id_seq", "session_starts_id_seq"}

var telemetryStageItems = append(append([]string{}, telemetryStageTables...), telemetryStageSeqs...)

func doltAddSQL(tables []string) string {
	quoted := make([]string, len(tables))
	for i, tbl := range tables {
		quoted[i] = "'" + strings.ReplaceAll(tbl, "'", "''") + "'"
	}
	return "SELECT dolt_add(" + strings.Join(quoted, ",") + ")"
}

func (r *Repo) Commit(ctx context.Context, msg string) (string, error) {
	var hash string
	err := Retry(ctx, func() error {
		if _, err := r.pool.Exec(ctx, doltAddSQL(auditStageTables)); err != nil {
			return fmt.Errorf("dolt_add: %w", err)
		}
		var h *string
		if err := r.pool.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`, msg).Scan(&h); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				hash = ""
				return nil
			}
			if isNothingToCommit(err) {

				hash = ""
				return nil
			}
			return fmt.Errorf("dolt_commit: %w", err)
		}
		if h != nil {
			hash = *h
		}
		return nil
	}, DefaultDelays)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func (r *Repo) CommitTelemetry(ctx context.Context, cmd string) error {
	if _, err := r.pool.Exec(ctx, doltAddSQL(telemetryStageItems)); err != nil {
		return fmt.Errorf("dolt_add(telemetry): %w", err)
	}
	var h *string
	if err := r.pool.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`,
		"telemetry: "+cmd).Scan(&h); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isNothingToCommit(err) {
			return nil
		}
		return fmt.Errorf("dolt_commit(telemetry): %w", err)
	}
	return nil
}

func execSQLScript(ctx context.Context, tx pgx.Tx, script string) error {
	for _, stmt := range splitStatements(script) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func splitStatements(script string) []string {
	var out []string
	for _, stmt := range strings.Split(script, ";") {
		var lines []string
		for _, ln := range strings.Split(stmt, "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), "--") {
				continue
			}
			lines = append(lines, ln)
		}
		if s := strings.TrimSpace(strings.Join(lines, "\n")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func mapInsertErr(err error) error {
	if isPgCode(err, "23505") || isConstraintRollback(err) {
		return fmt.Errorf("%w: %v", store.ErrDuplicate, err)
	}
	return err
}

func isConstraintRollback(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		strings.Contains(pgErr.Message, "constraint violations, transaction rolled back")
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func isNothingToCommit(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "XX000" &&
		strings.Contains(pgErr.Message, "nothing to commit")
}
