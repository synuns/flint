// repo.go — the flint repository access layer. Schema migration·
// insertion·dolt commits.
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

// sqlfs seam — tests swap in fake DDL (SUPERLITE_REAL=1 uses the real
// implementation).
var (
	schemaSQLFn = sqlfs.SchemaSQL
	viewsSQLFn  = sqlfs.ViewsSQL
)

// reapplyViews seam — gate tests count reapplication occurrences.
var reapplyViewsFn = (*Repo).reapplyViews

// kind → target table. write is INSERT-only, so each kind writes to a
// single table.
var kindTables = map[string]string{
	"knowledge": "knowledge",
	"session":   "sessions",
	"project":   "projects",
}

// Repo is the repository wrapping the Doltgres pool.
type Repo struct {
	pool *pgxpool.Pool
	dsn  string // needed for the simple-protocol connection used by view reapplication
}

// NewRepo connects and returns a Repo. Failures propagate as
// ErrServerDown.
func NewRepo(ctx context.Context, dsn string) (*Repo, error) {
	p, err := Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Repo{pool: p, dsn: dsn}, nil
}

// Close tears down the pool.
func (r *Repo) Close() { r.pool.Close() }

// Migrate runs SchemaSQL when the schema is absent and records
// meta.schema_version=1. The version gate is an integer comparison:
// schema_version>=1 means the tables already exist (v2 promotion belongs
// to admin.go EnsureSchemaV2 — the old gate checking only the string
// '1' failed with a nil wrap on v2 DBs). Views are gated on freshness
// (views_hash) independently of schema_version — with the same version,
// an updated view definition (stale active_knowledge) is reapplied on
// hash mismatch, but an already-current one is not rewritten.
func (r *Repo) Migrate(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("migrate: check schema_version: %w", err)
	}
	// Recreate when meta is absent (first creation) or the value does not
	// read as an integer.
	tablesCurrent := false
	if err == nil {
		if n, perr := strconv.Atoi(version); perr == nil && n >= 1 {
			tablesCurrent = true
		}
	}
	if !tablesCurrent { // first creation or version mismatch: tables + meta record
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
	// Views, unlike tables, are reapplied outside the transaction over
	// the simple protocol. But a DROP+CREATE on every command rewrites
	// dolt_schemas each time, which caused working-set merge conflicts in
	// parallel writes on fresh DBs. Skip when the views are already
	// current — currency is a double gate of ①the deployed SQL digest
	// (views_hash) ②the actual view column signature (views_sig, a
	// snapshot right after reapplication). ① catches view-definition
	// update deployments; ② catches views gone stale externally (stale
	// active_knowledge, the incident). Concurrent first runs on a fresh
	// DB can merge-conflict with each other's reapplication, so recovery
	// is by retry (absorbing another writer's completed reapplication as
	// viewsCurrent inside the loop). Fresh-DB contention can last
	// several seconds, so a longer backoff than Commit's default spacing
	// (0s/1s/3s) is used (measured: cases exist where the default 4
	// attempts did not recover).
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

// viewsCurrent — true when both the deployed SQL digest and the actual
// view signature match the recorded values. A lookup failure (first
// creation·missing views·external tampering) resolves to false, leading
// to reapplication.
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

// viewsSig — collects each view's actual column order as
// "name:col1,col2\n" lines. A missing view is an error (triggering
// reapplication). Read via the field metadata of SELECT * LIMIT 0 —
// Doltgres does not expose view columns in information_schema.
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

// viewNames — extracts the CREATE OR REPLACE VIEW target names from the
// SQL script.
var viewNameRe = regexp.MustCompile(`(?is)CREATE\s+OR\s+REPLACE\s+VIEW\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

func viewNames(script string) []string {
	matches := viewNameRe.FindAllStringSubmatch(script, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// hashSQL — the view-definition digest (SHA-256 hex).
func hashSQL(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

// reapplyViews recreates the views with the latest definitions (tables·
// data inviolable). Doltgres's extended protocol sometimes fails to
// update a view's column list on CREATE OR REPLACE VIEW (the stale
// active_knowledge incident), so — same as e2e mustDB — it runs DROP
// VIEW IF EXISTS then per-statement CREATE on a simple-protocol
// connection.
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
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		return fmt.Errorf("views: drop: %w", err)
	}
	for _, stmt := range splitStatements(viewsSQLFn()) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("views: create: %w", err)
		}
	}
	return nil
}

// batchStmtChunk — the statements per pgx Batch round trip in
// InsertBatch. Replaces the insertOne per-statement round-trip loop
// with chunked pipelining (cutting round trips on bulk accrual). Tests
// shrink the boundary to verify batch-over-chunk behavior·error
// atomicity (repo_test.go overrideBatchStmtChunk).
var batchStmtChunk = 1000

// insertStmt — one record's assembled INSERT ... RETURNING id.
type insertStmt struct {
	sql  string
	args []any
}

// buildInsertStmt — builds the per-kind table INSERT statement from
// rec. Columns are sorted (stable SQL string — for query cache·log
// comparison), and identifiers are quoted at the DB layer too (defense
// in depth against the structural risk of the guard existing only in
// the CLI column whitelist).
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

// InsertBatch inserts the records in a single transaction
// (all-or-nothing). Each record picks its target table with the "kind"
// key; the remaining key-values become column-values. Unique violations
// (content_hash·project name) are converted to store.ErrDuplicate. The
// insertOne per-statement round-trip loop was replaced with pgx Batch
// chunk (batchStmtChunk statements) pipelining — RETURNING id order
// preserved, the error contract (23505 → ErrDuplicate wrap·full
// rollback 0 rows on failure) unchanged.
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

// quoteIdent — SQL identifier quoting (" doubling — the same rule as
// dump.go).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// DupIDByHash returns the existing row id and dup=true when the
// content_hash is already in knowledge (dedup precheck + duplicate-
// response id exposure). Absent yields (0, false, nil).
// DupIDByHash — the backward-compatible path needing only the id (the
// ExistsHash adapter·DupIDStore fallback). Duplicate query resolution —
// the verdict body delegates to DupRowByHash.
func (r *Repo) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	id, _, _, exists, err := r.DupRowByHash(ctx, hash)
	return id, exists, err
}

// DupRowByHash — the existing-row metadata lookup for duplicate lines.
// At most 1 row since content_hash is UNIQUE. status·invalid_at
// irrelevant (invalidated rows also block identical re-recording — the
// NOOP intent). The single source of the dedup verdict.
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

// ExistsHash is the backward-compatible wrapper of DupIDByHash (dup
// presence only — the root.go adapter).
func (r *Repo) ExistsHash(ctx context.Context, hash string) (bool, error) {
	_, dup, err := r.DupIDByHash(ctx, hash)
	return dup, err
}

// ExistsProject returns whether the name already exists in projects
// (the project dedup precheck — the natural-key counterpart of the
// knowledge content_hash, unique(name)).
func (r *Repo) ExistsProject(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE name = $1)`, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("exists_project: %w", err)
	}
	return exists, nil
}

// transitionExecFn — the state-transition UPDATE execution seam. Tests
// (fakes) intercept the SQL·parameters and rows affected; the real
// implementation is pool.Exec (with SUPERLITE_REAL=1 the real
// implementation runs unswapped — see repo_transition_test).
var transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
	ct, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// Invalidate is the single update path of the knowledge invalidation
// transition: status='invalidated'·invalid_at=now() written together.
// The single source — the maintain invalidate-stale·--ids·expire-pending·
// --id paths all go through this method. The invalid_at IS NULL guard
// in the WHERE: already-processed rows (rows where a parallel Invalidate·
// Supersede stamped invalid_at) are not re-updated — blocking the
// corruption of overwriting the supersede lineage (invalid_at=NEW's
// valid_at) with now() in the race between listing (pending·stale —
// all targets selected with invalid_at IS NULL) and confirmation
// (convention-matching — the same guard in listExpiredPending·
// invalidate-stale listing and the admin.go succession invalidation).
// A rows-affected steward is built in: 0 rows (a race between the
// precheck and the write — parallel invalidation winning) becomes an
// ErrNotFound-wrapped error, blocking silent success (inheriting the
// existing supersede steward contract). The dolt commit is the caller's
// responsibility (per-path message).
func (r *Repo) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil // empty batch — no update (not a race)
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

// Supersede is the single update path of the knowledge supersede
// transition: status='invalidated'·superseded_by=newID·invalid_at=at
// (NEW's valid_at — per Zep t_invalid=t_valid) written together. The
// lineage is the single superseded_by model — a rows-affected steward
// is built in: 0 rows (a race after the precheck — OLD is no longer
// active) becomes an ErrNotFound-wrapped error.
func (r *Repo) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	n, err := transitionExecFn(ctx, r, `
		UPDATE knowledge
		SET status = 'invalidated',
		    superseded_by = $2,
		    invalid_at = $3
		WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`,
		oldID, newID, at)
	if err != nil {
		return 0, fmt.Errorf("supersede: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: supersede: id=%d update not applied (race — retry)", ErrNotFound, oldID)
	}
	return n, nil
}

// SetSupersededRef is the single path restoring the lineage reference
// of an already-invalidated snapshot row (import --format jsonl): not a
// state transition — it only fills the superseded_by reference
// (transitions are Supersede's exclusive domain). Updates only
// unreferenced rows (NULL), keeping rerun idempotence; 0 rows
// (reference already present·row absent) is not an error.
func (r *Repo) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	n, err := transitionExecFn(ctx, r, `
		UPDATE knowledge
		SET superseded_by = $2
		WHERE id = $1 AND superseded_by IS NULL`,
		oldID, newID)
	if err != nil {
		return 0, fmt.Errorf("superseded_ref: %w", err)
	}
	return n, nil
}

// PruneInvalidated is the single path DELETEing invalidated knowledge
// past N days: maintain --prune-invalidated --commit delegates to it.
// Deleting 0 rows is a normal result (the race between dry-run and
// confirmation is also reported as "0 deleted" — unlike the
// Invalidate-class 0-row steward, target disappearance is not an
// error). Orphan cleanup (R42-C): subqueries with the same conditions
// as the knowledge DELETE first remove residual recall_events (audit —
// absorbed by the audit commit)·knowledge_reads (metering — recovered
// by CommitTelemetry) rows — otherwise history rows survive without
// their source rows and compound recall_events growth. Bound in the
// same transaction block (the Merge convention — blocking partial
// deletion). The ordering is race-safe: invalidated rows are excluded
// from hot/read exposure (the status='active' exposure rule), so no new
// recall events accrue once deletion targets are settled. The dolt
// commit is the caller's responsibility (per-path message).
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

// MetaDelete is the single path deleting meta rows: tuning reset·
// reset --all delegate to it. Empty keys is a no-op (0, nil). Deleting
// 0 rows (already defaults) is also normal — reset is an idempotent
// contract.
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

// UpdateSessionSummary is the single sessions-update path for the done
// exit summary: writeSessionRecord (the session-count-invariant update)
// delegates to it. No updated-0-rows steward — as in the existing
// contract, only the result is returned quietly (non-interference §13 —
// failures on the done path are absorbed by the caller as one stderr
// line).
func (r *Repo) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	n, err := transitionExecFn(ctx, r,
		`UPDATE sessions SET summary = $1, tasks = $2 WHERE id = $3`,
		summary, tasks, id)
	if err != nil {
		return 0, fmt.Errorf("update_session_summary: %w", err)
	}
	return n, nil
}

// auditStageTables — the staging targets of the audit commit (Commit).
// Metering tables (read_events·hot_events·knowledge_reads·
// session_starts) are excluded — CommitTelemetry commits them
// separately, preserving the audit commit's diff purity. Ruling
// including recall_events (v1.9.0 T3): recall events (INSERT-only) must
// be absorbed by the audit commit ("read: recall bump") to propagate
// across devices via push/pull — they are audit data, not metering
// tables (the premise of multi-device synchronization). Ruling
// including sessions: the done session-end commit keeps session-row
// attribution. dolt_add stages only the named tables when tables are
// specified (measured: '*_id_seq is a separate item and not swept along
// by table staging — audit-sequence residue stays in the working set.
// v1.7.2 T1: with the global sweep abolished, the recovery owner is
// gone·harmless (a dolt_status residue display only; metering·lookups
// unchanged)).
var auditStageTables = []string{"knowledge", "projects", "sessions", "meta", "maintenance_runs", "recall_events"}

// telemetryStageTables — the staging targets of the metering-only
// commit (CommitTelemetry). The schema v13 observability tables
// (threshold_snapshots·weekly_rollups) join the explicit list — neither
// adopts a serial id column, so no *_id_seq entry is needed (the no-
// global-dolt_add principle: an unstaged metering table never reaches
// the telemetry commit).
var telemetryStageTables = []string{"read_events", "hot_events", "knowledge_reads", "session_starts", "threshold_snapshots", "weekly_rollups"}

// telemetryStageSeqs — the metering sequences CommitTelemetry stages
// along (v1.7.2 T1, R38-C1). Each is the serial id source of a metering
// table, incrementing on every INSERT, and dolt tracks sequences as
// items separate from tables (measured — dolt_add('read_events') does
// not stage read_events_id_seq, and dolt_add('read_events_id_seq')
// stages it standalone). The global dolt_add('-A') sweep absorbed even
// unstaged changes of audit tables (recall bumps etc.) into the
// metering commit, breaking audit attribution, so it was abolished and
// only these 4 are staged explicitly.
var telemetryStageSeqs = []string{"read_events_id_seq", "hot_events_id_seq", "knowledge_reads_id_seq", "session_starts_id_seq"}

// telemetryStageItems — the dolt_add arguments of CommitTelemetry
// (tables+sequences combined, assembled once at package init).
var telemetryStageItems = append(append([]string{}, telemetryStageTables...), telemetryStageSeqs...)

// doltAddSQL — assembles the dolt_add literal. The dolt-family table
// functions take no parameterized arguments (measured) — target names
// are package constants (no external input) inlined with single-quote
// escaping.
func doltAddSQL(tables []string) string {
	quoted := make([]string, len(tables))
	for i, tbl := range tables {
		quoted[i] = "'" + strings.ReplaceAll(tbl, "'", "''") + "'"
	}
	return "SELECT dolt_add(" + strings.Join(quoted, ",") + ")"
}

// Commit stages and commits only the audit tables (knowledge·projects·
// sessions·meta·maintenance_runs) (the global dolt_add('-A') abolished —
// metering tables belong to CommitTelemetry). Returns the commit hash
// and Retries with the default delays (0s/1s/3s) with conflict
// possibility in mind.
func (r *Repo) Commit(ctx context.Context, msg string) (string, error) {
	var hash string
	err := Retry(ctx, func() error {
		if _, err := r.pool.Exec(ctx, doltAddSQL(auditStageTables)); err != nil {
			return fmt.Errorf("dolt_add: %w", err)
		}
		var h *string // NULL when there is nothing to commit
		if err := r.pool.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`, msg).Scan(&h); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				hash = ""
				return nil
			}
			if isNothingToCommit(err) {
				// When a parallel writer's dolt_add already committed even
				// this session's changes (measured) — the data is persisted,
				// so treat as success.
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

// CommitTelemetry stages only the metering tables (telemetryStageTables
// — the 4 event tables plus the schema v13 observability pair) and the
// 4 metering sequences (telemetryStageSeqs) and makes the 'telemetry: <cmd>'
// commit. Independent of the audit commit (Commit) — the read/hot/done
// completion paths call it once. The global dolt_add('-A') sweep is
// abolished — the sweep absorbed not just sequence residue but also
// unstaged changes of audit tables into the metering commit, breaking
// audit attribution (measured). Metering sequences are staged from an
// explicit list, and the residue of audit-table sequences
// (knowledge_id_seq etc.) stays in the working set with no recovery
// owner (harmless — a dolt_status display only). The metering principle
// (§13): failures are absorbed quietly by the caller. dolt_add has
// already staged everything, so the failed-commit portion is recovered
// by the next call (no loss). Staging 0 rows (nothing to commit) is not
// an error.
func (r *Repo) CommitTelemetry(ctx context.Context, cmd string) error {
	if _, err := r.pool.Exec(ctx, doltAddSQL(telemetryStageItems)); err != nil {
		return fmt.Errorf("dolt_add(telemetry): %w", err)
	}
	var h *string // NULL when there is nothing to commit
	if err := r.pool.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`,
		"telemetry: "+cmd).Scan(&h); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isNothingToCommit(err) {
			return nil
		}
		return fmt.Errorf("dolt_commit(telemetry): %w", err)
	}
	return nil
}

// execSQLScript runs a semicolon-separated SQL script statement by
// statement — because Doltgres does not allow multiple statements in
// one Exec.
func execSQLScript(ctx context.Context, tx pgx.Tx, script string) error {
	for _, stmt := range splitStatements(script) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a SQL blob into statements. Leading '--'
// comment lines are removed (safe for Doltgres simple Exec) and empty
// statements dropped. The flint schema SQL uses no semicolons inside
// string literals.
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

// mapInsertErr wraps unique violations as store.ErrDuplicate. Two
// paths: (1) SQLSTATE 23505 (an ordinary unique violation), (2) the
// Doltgres commit-time race violation — when, under concurrent writes,
// two sessions that passed the precheck accrue the same content_hash,
// the later one's commit discovers a working-set constraint violation
// and rolls back with code XX000 and the message "constraint
// violations, transaction rolled back" (measured 2026-11-17). Without
// classifying (2), write's survive path (recheck skip + retry of the
// remainder) is never reached and the race loser's unique lines are
// lost.
func mapInsertErr(err error) error {
	if isPgCode(err, "23505") || isConstraintRollback(err) {
		return fmt.Errorf("%w: %v", store.ErrDuplicate, err)
	}
	return err
}

// isConstraintRollback — the verdict for Doltgres commit-time
// constraint-violation rollbacks. Even a unique violation arrives as
// XX000 rather than SQLSTATE 23505 (measured), so it is classified by
// message. Same XX000 class as isNothingToCommit but with a different
// message.
func isConstraintRollback(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		strings.Contains(pgErr.Message, "constraint violations, transaction rolled back")
}

// isPgCode — whether a PgError in the err chain carries the given
// SQLSTATE.
func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// isNothingToCommit — dolt_commit's "nothing to commit" (XX000). Means
// the staging is empty; under parallel writes it indicates another
// writer committed on our behalf.
func isNothingToCommit(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "XX000" &&
		strings.Contains(pgErr.Message, "nothing to commit")
}
