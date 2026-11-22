// admin.go — updates·aggregations. approve·invalidate·merge are the
// only update-permitted paths (the global constraint: write is
// INSERT-only, no UPDATE except via curation — approval/invalidation/
// merging are spec §8 curation paths, leaving the reason in the dolt
// commit message). The call order·latest version of the promotion gates
// EnsureSchemaV* are owned by the schema_gates.go registry as a single
// source (adding only a function here makes the CoverAdmin guard demand
// its registration).
package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound — the target id does not exist (CLI exit 1 mapping).
var ErrNotFound = errors.New("flint: not found")

// Counts — aggregates for the maintain --check report.
type Counts struct {
	Pending    int // count of status=pending knowledge
	DupSuspect int // count of duplicate-suspect items with 2+ active rows sharing a topic
}

// gateRetryBackoff — the 40001 (serialization failure) retry spacing
// for promotion commits. Under parallel startup (e.g. e2e with 8
// concurrent writers), a promotion commit can conflict with another
// writer's, so recovery uses the same backoff as views reapplication
// (repo.go Migrate). When another writer completes the promotion during
// a retry, each gate's numeric-comparison short circuit becomes an
// immediate no-op (CI run 39717583172).
var gateRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 9 * time.Second}

// EnsureSchemaV2 losslessly promotes a DB with meta.schema_version<2 to
// v2: adds the sessions.tasks·projects.note columns with ADD COLUMN IF
// NOT EXISTS and records version 2 (repo.go Migrate is v1-owned and
// does not touch this). Already v2+ skips even opening the
// promotion transaction (the numeric-comparison short circuit — when it
// was a string-equality check, higher-version DBs still opened the
// promotion transaction, rolling the version back and widening the 40001
// window under parallel startup). Idempotent on fresh DBs too (created
// by schema.sql at v2).
func (r *Repo) EnsureSchemaV2(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV2(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV2(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v2: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		// No meta table at all (a fresh DB) — the gate skips; once Migrate
		// creates the schema, the second gate (newRealStore) promotes.
		return nil
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 2 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v2: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tasks text`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS note text`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v2: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '2')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v2: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v2: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV3 losslessly promotes a DB with meta.schema_version<3 to
// v3: creates the 3 knowledge filter-path (entity·domain·status)
// B-tree indexes with CREATE INDEX IF NOT EXISTS and records version 3
// (ADR-0008: %q% free text cannot use a B-tree, so a sequential scan is
// accepted). Already v3+ is a transaction-free·commit-free no-op (the
// numeric-comparison short circuit), idempotent on any DB that passed
// the v1·v2 gates.
func (r *Repo) EnsureSchemaV3(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV3(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV3(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v3: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 3 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v3: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS flint_knowledge_entity_idx ON knowledge (entity)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_domain_idx ON knowledge (domain)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_status_idx ON knowledge (status)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v3: index: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '3')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v3: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v3: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV4 losslessly promotes a DB with meta.schema_version<4 to
// v4: adds the knowledge reference-metering columns (read_count·
// last_read_at) with ADD COLUMN IF NOT EXISTS and records version 4
// (spec §2: no new indexes — reuses the status index; sequential scan
// accepted at personal scale). Pre-existing rows get the defaults 0/
// null. Already v4 is a transaction-free·commit-free no-op (the
// numeric-comparison short circuit), idempotent on any DB that passed
// the v1~v3 gates.
func (r *Repo) EnsureSchemaV4(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV4(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV4(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v4: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 4 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v4: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS read_count integer NOT NULL DEFAULT 0`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS last_read_at timestamptz`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v4: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '4')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v4: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v4: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV5 losslessly promotes a DB with meta.schema_version<5 to
// v5: adds the knowledge lineage column (superseded_by) with ADD COLUMN
// IF NOT EXISTS and records version 5 (spec v0.8 §2: batch
// invalidation stamps the replacing knowledge id. Views unchanged —
// the column does not affect the existing view definitions).
// Pre-existing rows get the default null. Already v5+ is a
// transaction-free·commit-free no-op (the numeric-comparison short
// circuit — string comparison forbidden, preventing a recurrence of the
// downgrade incident), idempotent on any DB that passed the v1~v4
// gates.
func (r *Repo) EnsureSchemaV5(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV5(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV5(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v5: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 5 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v5: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS superseded_by int`); err != nil {
		return fmt.Errorf("schema v5: alter: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '5')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v5: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v5: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV6 losslessly promotes a DB with meta.schema_version<6 to
// v6: adds the effectiveness-metering event-log table read_events with
// CREATE TABLE IF NOT EXISTS (docs/METRICS.md — existing tables·views
// unchanged, additive). Pre-promotion DBs get an empty log. Already v6+
// is a transaction-free·commit-free no-op (the numeric-comparison short
// circuit — preventing a recurrence of the downgrade incident),
// idempotent on any DB that passed the v1~v5 gates.
func (r *Repo) EnsureSchemaV6(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV6(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV6(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v6: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 6 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v6: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS read_events (
		    id        serial PRIMARY KEY,
		    ts        timestamptz NOT NULL DEFAULT now(),
		    hits      integer NOT NULL,
		    zero      boolean NOT NULL,
		    near      boolean NOT NULL,
		    fallback  boolean NOT NULL,
		    session   text NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("schema v6: create: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '6')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v6: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v6: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV8 losslessly promotes a DB with meta.schema_version<8 to
// v8: hit-quality approximation (METRICS §2) — adds the first_rank
// column to read_events (ALTER, additive). Existing tables·views
// unchanged. Already v8+ is a no-op, idempotent on any lower-version
// DB.
func (r *Repo) EnsureSchemaV8(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV8(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV8(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v8: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB — the fresh-creation path includes the v8 column
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 8 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v8: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS first_rank integer NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("schema v8: alter read_events: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '8')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v8: version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v8: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV7 losslessly promotes a DB with meta.schema_version<7 to
// v7: metering refinement (docs/METRICS.md §4) — adds the ids column
// to read_events (ALTER)·creates the session_starts·hot_events tables
// (CREATE IF NOT EXISTS). Existing tables·views unchanged (additive).
// Already v7+ is a no-op (the numeric-comparison short circuit),
// idempotent on any DB that passed the v1~v6 gates.
func (r *Repo) EnsureSchemaV7(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV7(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV7(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v7: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 7 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v7: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS ids text NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("schema v7: alter read_events: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS session_starts (
		    id      serial PRIMARY KEY,
		    ts      timestamptz NOT NULL DEFAULT now(),
		    session text NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("schema v7: create session_starts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS hot_events (
		    id  serial PRIMARY KEY,
		    ts  timestamptz NOT NULL DEFAULT now(),
		    ids text NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("schema v7: create hot_events: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '7')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v7: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v7: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV9 losslessly promotes a DB with meta.schema_version<9 to
// v9: 4 metering·derived-output query-path indexes — 3 time-window
// scan indexes read_events(ts)·hot_events(ts)·session_starts(ts) and 1
// knowledge pending partial (status WHERE status='pending' AND
// invalid_at IS NULL — the Counts pending aggregation path) — created
// with CREATE INDEX IF NOT EXISTS, recording version 9. Existing
// tables·views·data unchanged (additive). Already v9+ is a no-op (the
// numeric-comparison short circuit), idempotent on any DB that passed
// the v1~v8 gates.
func (r *Repo) EnsureSchemaV9(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV9(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV9(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v9: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 9 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v9: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS flint_read_events_ts_idx ON read_events (ts)`,
		`CREATE INDEX IF NOT EXISTS flint_hot_events_ts_idx ON hot_events (ts)`,
		`CREATE INDEX IF NOT EXISTS flint_session_starts_ts_idx ON session_starts (ts)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_pending_idx ON knowledge (status)
		 WHERE status = 'pending' AND invalid_at IS NULL`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v9: index: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '9')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v9: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v9: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV10 losslessly promotes a DB with meta.schema_version<10
// to v10 (spec §1.2 — the observability-upgrade storage base,
// additive): adds the 3 read_events columns bytes (output bytes)·
// zero_reason ('unowned'|'owned_miss'|”)·source ('hook'|'cli'|'mcp')
// with ADD COLUMN IF NOT EXISTS, creates the per-item recall-history
// table knowledge_reads (knowledge_id·ts·session·kind) with CREATE
// TABLE IF NOT EXISTS, and adds the ts index. Pre-promotion read_events
// rows get the defaults 0·”·”. Existing tables·views·data unchanged.
// Already v10+ is a no-op (the numeric-comparison short circuit),
// idempotent on any DB that passed the v1~v9 gates.
func (r *Repo) EnsureSchemaV10(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV10(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV10(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v10: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 10 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v10: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS bytes integer NOT NULL DEFAULT 0`,
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS zero_reason text NOT NULL DEFAULT ''`,
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS knowledge_reads (
		    id           serial PRIMARY KEY,
		    knowledge_id integer NOT NULL,
		    ts           timestamptz NOT NULL DEFAULT now(),
		    session      text NOT NULL DEFAULT '',
		    kind         text NOT NULL DEFAULT 'read'
		)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_reads_ts_idx ON knowledge_reads (ts)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v10: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '10')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v10: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v10: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV11 losslessly promotes a DB with
// meta.schema_version<11 to v11 (the decay/reinforcement score source,
// additive): adds the 2 knowledge columns recall_score (integer,
// default 0)·last_recalled_at (timestamptz NULL) with ADD COLUMN IF
// NOT EXISTS. Pre-existing rows get the defaults 0·NULL. Since both
// columns carry defaults, older binaries (v10) read a v11 DB as-is
// (backward compatible). Existing tables·views·data unchanged. Already
// v11+ is a no-op (the numeric-comparison short circuit), idempotent
// on any DB that passed the v1~v10 gates. A DB that already has the
// recall_events table (a fresh DB created at the schema.sql v12
// terminus) skips the column additions and heads straight for the v12
// gate — avoiding the add-then-immediately-drop DDL round trip (R43).
func (r *Repo) EnsureSchemaV11(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV11(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV11(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v11: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 11 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v11: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	// The fresh-DB direct short circuit (R43) — if the recall_events
	// table already exists, this DB is a fresh one created at the
	// schema.sql v12 terminus (version still 3 — the chain is promoting
	// from v4): it skips the DDL round trip of adding the 2 v11 columns
	// only for the v12 gate to drop them. An existing v11 DB has no
	// recall_events (the v12 gate creates it first), so the column
	// additions proceed as before — becoming the backfill source of the
	// v12 promotion.
	var hasRecallEvents int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_name = 'recall_events'`).Scan(&hasRecallEvents); err != nil {
		return fmt.Errorf("schema v11: check recall_events: %w", err)
	}
	if hasRecallEvents == 0 {
		for _, stmt := range []string{
			`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS recall_score integer NOT NULL DEFAULT 0`,
			`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS last_recalled_at timestamptz`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("schema v11: alter: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '11')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v11: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v11: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV12 losslessly promotes a DB with
// meta.schema_version<12 to v12 (multi-device stabilization — the
// recall append-only source switch): creates the recall-history-only
// recall_events (knowledge_id·recalled_at) table+index and drops the
// knowledge recall_score·last_recalled_at columns (the read path moved
// to the event-derived join — v1.9.0). Pre-promotion scores are
// backfilled as synthetic events, one per score count (recalled_at =
// COALESCE(last_recalled_at, now())) — synthetics preserving score
// continuity, not actual event history (the event source is exact from
// v12 on). Doltgres lacks DROP COLUMN IF EXISTS (measured in R40), so
// it checks information_schema.columns before dropping. Already v12+
// is a no-op (the numeric-comparison short circuit) and idempotent.
func (r *Repo) EnsureSchemaV12(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV12(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV12(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v12: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — see the ensureSchemaV2 42P01 comment
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 12 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v12: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS recall_events (
		    knowledge_id integer NOT NULL,
		    recalled_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_recall_events_id_time
		 ON recall_events (knowledge_id, recalled_at DESC)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v12: create: %w", err)
		}
	}
	// Backfill·column drop — only when recall_score exists (under
	// partial promotion etc., if the column is already absent, the
	// backfill is skipped — because the SELECT fails with 42703).
	var hasScore int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'knowledge' AND column_name = 'recall_score'`).Scan(&hasScore); err != nil {
		return fmt.Errorf("schema v12: check recall_score: %w", err)
	}
	if hasScore == 1 {
		// Synthetic-event backfill — one row per score count (score
		// continuity preserved. Not actual recall history — see the
		// comment above).
		if _, err := tx.Exec(ctx,
			`INSERT INTO recall_events (knowledge_id, recalled_at)
			 SELECT k.id, COALESCE(k.last_recalled_at, now())
			 FROM knowledge k, generate_series(1, k.recall_score) g
			 WHERE k.recall_score > 0`); err != nil {
			return fmt.Errorf("schema v12: backfill: %w", err)
		}
		// Column drop — Doltgres lacks DROP COLUMN IF EXISTS (measured in
		// R40), so it checks information_schema first (the same pattern
		// as hasScore above).
		for _, col := range []string{"recall_score", "last_recalled_at"} {
			var n int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM information_schema.columns
				 WHERE table_name = 'knowledge' AND column_name = $1`, col).Scan(&n); err != nil {
				return fmt.Errorf("schema v12: check column %s: %w", col, err)
			}
			if n == 0 {
				continue
			}
			if _, err := tx.Exec(ctx,
				`ALTER TABLE knowledge DROP COLUMN `+col); err != nil {
				return fmt.Errorf("schema v12: drop %s: %w", col, err)
			}
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '12')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v12: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v12: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV13 losslessly promotes a DB with
// meta.schema_version<13 to v13 (v1.19.0 observability foundation —
// additive): adds the read_events column near_score (double precision,
// NOT NULL DEFAULT 0 — the scan-max bigramDice score of zero rows
// regardless of the near flag; near=false rows are not masked to 0, the
// min_score-boundary distribution being the counterfactual-analysis
// core) with ADD COLUMN IF NOT EXISTS (the v10 column-extension
// precedent), and creates the 2 metering tables with CREATE TABLE IF
// NOT EXISTS (the v12 precedent): threshold_snapshots (the append-only
// threshold-change history — ts·key·old_value·new_value; the fired
// motivation column is deferred to the v1.23.0 registry) with its
// (key, ts DESC) index, and weekly_rollups (the long-baseline weekly
// num/den counters — no derived ratios, PK (week_start, metric)).
// Neither table adopts a serial id column, so no sequence staging is
// needed. Pre-promotion read_events rows get the default 0. Existing
// tables·views·data unchanged (no existing column is altered). Already
// v13+ is a no-op (the numeric-comparison short circuit), idempotent on
// any DB that passed the v1~v12 gates.
func (r *Repo) EnsureSchemaV13(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV13(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV13(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v13: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v13 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 13 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v13: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE read_events ADD COLUMN IF NOT EXISTS near_score double precision NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS threshold_snapshots (
		    ts        timestamptz NOT NULL DEFAULT now(),
		    key       text NOT NULL,
		    old_value text NOT NULL,
		    new_value text NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_threshold_snapshots_key_time
		 ON threshold_snapshots (key, ts DESC)`,
		`CREATE TABLE IF NOT EXISTS weekly_rollups (
		    week_start date   NOT NULL,
		    metric     text   NOT NULL,
		    num        bigint NOT NULL,
		    den        bigint NOT NULL,
		    PRIMARY KEY (week_start, metric)
		)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v13: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '13')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v13: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v13: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV14 losslessly promotes a DB with
// meta.schema_version<14 to v14 (v1.20.0 knowledge links — additive):
// creates the knowledge_links table (INSERT-only write principle — no
// UPDATE/DELETE path; from/to existence is checked on the write path,
// not by FK, the existing knowledge convention) with CREATE TABLE IF
// NOT EXISTS (the v12·v13 precedent), its (from_id)·(to_id) indexes,
// and the knowledge (superseded_by) index — the traversal axis of the
// recursive chain view·cycle CTE (existing indexes cover only
// entity·domain·status). Existing tables·views·data unchanged. Already
// v14+ is a no-op (the numeric-comparison short circuit), idempotent on
// any DB that passed the v1~v13 gates.
func (r *Repo) EnsureSchemaV14(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV14(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV14(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v14: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v14 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 14 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v14: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS knowledge_links (
		    id         bigserial PRIMARY KEY,
		    from_id    bigint NOT NULL,
		    to_id      bigint NOT NULL,
		    relation   text NOT NULL,
		    created_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_links_from_id
		 ON knowledge_links (from_id)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_links_to_id
		 ON knowledge_links (to_id)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_superseded_by
		 ON knowledge (superseded_by)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v14: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '14')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v14: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v14: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV15 losslessly promotes a DB with
// meta.schema_version<15 to 15: the knowledge_links uniqueness seal —
// a unique index on (from_id, to_id, relation) making the idempotent
// link accrual race-proof (v1.20.0 shipped only the NOT EXISTS guard,
// which leaves a check-then-insert window under parallel writers).
// Residual duplicate rows (accrued before the seal) are collapsed to
// the lowest id first — DELETE ... USING is unimplemented in Doltgres
// (measured), so the dedup is the NOT IN (MIN(id) GROUP BY triple)
// form. Additive: one index, no table changes.
func (r *Repo) EnsureSchemaV15(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV15(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV15(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v15: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v15 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 15 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v15: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		// Collapse pre-seal duplicates to the lowest id per triple before
		// the unique index (a duplicate row would fail the CREATE).
		`DELETE FROM knowledge_links WHERE id NOT IN (
		    SELECT MIN(id) FROM knowledge_links
		    GROUP BY from_id, to_id, relation)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_links_triple
		 ON knowledge_links (from_id, to_id, relation)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v15: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '15')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v15: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v15: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV16 losslessly promotes a DB with
// meta.schema_version<16 to 16 (v1.21.0 vault bridge — additive): the
// vault_files pointer-snapshot table (one row per vault .md file — the
// original text is never copied; path UNIQUE is the scan-diff UPSERT
// axis; content_hash is the sha256 of the raw bytes, deliberately
// distinct from the knowledge normalized hash) with its (content_hash)
// index — the duplicate-note detection axis (identical content under
// different paths). An audit staging target (auditStageTables — the
// recall_events ruling precedent: the vault index is a recall-domain
// source). Existing tables·views·data unchanged. Already v16+ is a
// no-op (the numeric-comparison short circuit), idempotent on any DB
// that passed the v1~v15 gates.
func (r *Repo) EnsureSchemaV16(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV16(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV16(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v16: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v16 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 16 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v16: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS vault_files (
		    id              bigserial PRIMARY KEY,
		    path            text NOT NULL UNIQUE,
		    title           text NOT NULL,
		    tags            text NOT NULL DEFAULT '',
		    mtime           timestamptz NOT NULL,
		    size            bigint NOT NULL,
		    content_hash    text NOT NULL,
		    summary         text NOT NULL DEFAULT '',
		    last_indexed_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_files_content_hash
		 ON vault_files (content_hash)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v16: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '16')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v16: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v16: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV17 losslessly promotes a DB with
// meta.schema_version<17 to 17 (v1.22.0 vault staleness II — additive):
// three columns — vault_files.content_changed_at (timestamptz NULL — the
// scan-diff recheck queue stamp, set by ApplyDiff on a changed row and
// cleared only on hash restoration), knowledge.distilled_at (timestamptz
// NULL — the distill output idempotence stamp: emitted rows are stamped
// and never re-emitted), and knowledge.conditions (text NOT NULL DEFAULT
// ” — the manual recheck tag, excluded from the content hash via
// hashVolatile). All three are plain ALTER ADDs (the v11/v13
// DEFAULT-ALTER precedent — the two timestamps backfill NULL, conditions
// backfills the empty string). Existing tables·views·data unchanged.
// Already v17+ is a no-op (the numeric-comparison short circuit),
// idempotent on any DB that passed the v1~v16 gates.
func (r *Repo) EnsureSchemaV17(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV17(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV17(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v17: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v17 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 17 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v17: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE vault_files ADD COLUMN IF NOT EXISTS content_changed_at timestamptz`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS distilled_at timestamptz`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS conditions text NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v17: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '17')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v17: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v17: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV18 losslessly promotes a DB with
// meta.schema_version<18 to 18 (v1.23.0 autotune shadow — additive):
// creates the shadow-prediction log tuning_predictions (append-only —
// one row per fired shadow round; trigger·key·cur·new·predicted
// zero_near_pct·reclass count·sample size·composed rationale, numbers
// and the registry vocabulary only — no personal data) with CREATE
// TABLE IF NOT EXISTS (the v12·v13 precedent) and its (ts) index, and
// adds the 3 threshold_snapshots columns origin·trigger·rationale with
// ADD COLUMN IF NOT EXISTS (the snapshot.go "deferred to v1.23.0"
// delivery — all NULL-able: v1.23.0 has 0 applies, so no origin=auto
// rows exist yet; pre-promotion rows backfill NULL). An audit staging
// target (auditStageTables — the recall_events ruling precedent: shadow
// predictions must propagate across devices). Existing tables·views·
// data unchanged. Already v18+ is a no-op (the numeric-comparison short
// circuit), idempotent on any DB that passed the v1~v17 gates.
func (r *Repo) EnsureSchemaV18(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV18(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV18(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v18: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // fresh DB (tables absent) — the fresh-creation path carries the v18 terminus
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 18 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v18: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tuning_predictions (
		    id                   bigserial PRIMARY KEY,
		    ts                   timestamptz NOT NULL DEFAULT now(),
		    trigger              text NOT NULL,
		    key                  text NOT NULL,
		    cur                  text NOT NULL,
		    new                  text NOT NULL,
		    pred_zero_near_pct   double precision NOT NULL,
		    pred_reclass_n       int NOT NULL,
		    sample_n             int NOT NULL,
		    rationale            text NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tuning_predictions_ts
		 ON tuning_predictions (ts)`,
		`ALTER TABLE threshold_snapshots ADD COLUMN IF NOT EXISTS origin text`,
		`ALTER TABLE threshold_snapshots ADD COLUMN IF NOT EXISTS trigger text`,
		`ALTER TABLE threshold_snapshots ADD COLUMN IF NOT EXISTS rationale text`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v18: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '18')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v18: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v18: commit: %w", err)
	}
	return nil
}

// Approve promotes pending knowledge to active (spec §8: approval =
// flint approve). Approval is a valid confirmation, so it also clears
// any residual invalid_at — defense against the defect where
// approving an invalidated pending produced an active+invalid_at zombie
// (a row visible neither in the view nor --status). Already active is
// an idempotent no-op; a missing id is ErrNotFound.
func (r *Repo) Approve(ctx context.Context, id int) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'active', invalid_at = NULL
		 WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	if ct.RowsAffected() == 1 {
		_, err := r.Commit(ctx, fmt.Sprintf(
			"approve: id=%d pending→active (approval update path) [session:0]", id))
		if err != nil {
			return fmt.Errorf("approve: commit: %w", err)
		}
		return nil
	}
	// Updated 0 rows: check the idempotence target — active is an
	// idempotent no-op; absent is ErrNotFound.
	var status string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("approve: lookup: %w", err)
	}
	if status == "active" {
		return nil
	}
	return fmt.Errorf("approve: id=%d unexpected status %q", id, status)
}

// Merge invalidates from (status·superseded_by·invalid_at written
// together — the same columns·update shape as Supersede, the same
// guarded UPDATE) and leaves a merge marker on to's summary (duplicate
// cleanup). The lineage is the single superseded_by model — the
// marker-suffix convention is kept, but the two updates are bound in
// one transaction block so lineage queries never lose it (blocking
// partial updates — marker only, superseded_by missing). Re-merging an
// already-invalidated from is refused by the 0-rows steward
// (inheriting the Supersede convention — preventing marker
// re-attachment). The supersede cycle guard applies too (Merge writes
// from.superseded_by=to): a reverse merge whose to chain already
// reaches from is rejected with the cycle diagnostic. v1.20.3 (r89):
// the whole precheck-then-write joins the supersede lock domain — the
// old pool-based precheck·tx sat OUTSIDE the serialization, so
// Supersede(A,B) ∥ Merge(B,A) both passed their guards and both
// landed, planting a 2-cycle (deterministically reproducible, 20/20).
func (r *Repo) Merge(ctx context.Context, fromID, toID int) error {
	exec, query, unlock, err := supersedeLockFn(ctx, r)
	if err != nil {
		return err
	}
	// R90 (v1.21.0): a panic in the lock-held body (or anywhere below)
	// would leak the advisory lock — the deferred release runs on the
	// unwind too (the SetSupersededRef convention). The unlock is
	// idempotent, so coexisting with the explicit release below (and the
	// callers' deferred release) is a no-op.
	defer unlock()
	err = r.mergeUnderSupersedeLock(ctx, exec, query, fromID, toID)
	// The dolt commit is pool-based: it runs AFTER the lock release —
	// under the lock, a pool of MaxConns lock waiters leaves no slot to
	// acquire and the commit would block (the r88 deadlock shape). The
	// unlock is idempotent, so the callers' deferred release is a no-op.
	unlock()
	if err != nil {
		return err
	}
	if _, err := r.Commit(ctx, fmt.Sprintf(
		"merge: from=%d into=%d (dedup merge update path) [session:0]", fromID, toID)); err != nil {
		return fmt.Errorf("merge: commit: %w", err)
	}
	return nil
}

// mergeUnderSupersedeLock — Merge's body while holding the supersede
// advisory lock: every statement (the cycle precheck, the guarded
// from-UPDATE, the steward lookup, the marker UPDATE, the tx
// BEGIN/COMMIT) runs on the lock-held connection — a pool hop under
// the lock is the r88 deadlock shape. The two updates stay bound in
// one transaction block (BEGIN/COMMIT through the same connection's
// exec; the rollback rides the background ctx so a canceled write
// still closes the tx).
func (r *Repo) mergeUnderSupersedeLock(ctx context.Context, exec supersedeExecFn, query supersedeQueryFn, fromID, toID int) error {
	if cyc, err := wouldCreateCycle(ctx, query, fromID, toID); err != nil {
		return fmt.Errorf("merge: %w", err)
	} else if cyc {
		return fmt.Errorf("merge: id=%d -> id=%d would create a supersede cycle", fromID, toID)
	}
	if _, err := exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("merge: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = exec(context.Background(), "ROLLBACK")
		}
	}()
	n, err := exec(ctx, supersedeGuardedSQL,
		fromID, toID, time.Now().UTC(), SupersedeCycleDepthLimit)
	if err != nil {
		return fmt.Errorf("merge: invalidate from: %w", err)
	}
	if n == 0 {
		// One steward lookup distinguishes existence and status together
		// (the same pattern as approve's 0-rows idempotence-target lookup
		// — the query count unchanged). invalidated means an
		// already-processed re-merge; statuses other than active are
		// reported unmergeable.
		var status string
		err := query(ctx,
			`SELECT status FROM knowledge WHERE id = $1`, fromID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("merge from: lookup: %w", err)
		}
		if status == "invalidated" {
			return fmt.Errorf("%w: merge: id=%d update not applied (from already processed — re-merge refused)",
				ErrNotFound, fromID)
		}
		if status == "active" {
			// The WHERE is status='active' AND invalid_at IS NULL — if the
			// status is active yet 0 rows updated, invalid_at was already
			// set (a corrupted state). Name the cause instead of the
			// self-contradictory "not active — status: active".
			return fmt.Errorf("%w: merge: id=%d update not applied (active but invalid_at already set — cannot merge)",
				ErrNotFound, fromID)
		}
		return fmt.Errorf("%w: merge: id=%d update not applied (not active (cannot merge) — status: %s)",
			ErrNotFound, fromID, status)
	}
	marker := fmt.Sprintf(" [merged:from=%d]", fromID)
	n, err = exec(ctx,
		`UPDATE knowledge SET summary = summary || $2
		 WHERE id = $1 AND summary NOT LIKE '%' || $2`, toID, marker)
	if err != nil {
		return fmt.Errorf("merge: mark to: %w", err)
	}
	if n == 0 {
		// When the marker is already a suffix, it is not re-attached
		// (idempotent pass) — the same existence check as ensureExists,
		// on the lock-held connection.
		var one int
		err := query(ctx,
			`SELECT 1 FROM knowledge WHERE id = $1`, toID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("merge to: lookup: %w", err)
		}
	}
	if _, err := exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("merge: tx commit: %w", err)
	}
	committed = true
	return nil
}

// Counts counts the pending count and the duplicate-suspect count
// (maintain --check input).
func (r *Repo) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge
		 WHERE status = 'pending' AND invalid_at IS NULL`).Scan(&c.Pending); err != nil {
		return c, fmt.Errorf("counts: pending: %w", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(sum(n), 0) FROM (
		    SELECT count(*) AS n FROM knowledge
		    WHERE invalid_at IS NULL AND status = 'active'
		    GROUP BY topic HAVING count(*) > 1
		 ) d`).Scan(&c.DupSuspect); err != nil {
		return c, fmt.Errorf("counts: dup suspect: %w", err)
	}
	return c, nil
}

// DupGroup — a duplicate group for the maintain --dup-suspects report
// (active rows sharing a topic).
type DupGroup struct {
	Topic string
	IDs   []int
}

// DupGroups returns groups of 2+ active rows sharing a topic (report
// only). The formula is the same GROUP BY as Counts' dup_suspect; ids
// ascending, groups topic-ascending with a limit cap.
func (r *Repo) DupGroups(ctx context.Context, limit int) ([]DupGroup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT topic, array_agg(id ORDER BY id) FROM knowledge
		WHERE invalid_at IS NULL AND status = 'active'
		GROUP BY topic HAVING count(*) > 1
		ORDER BY topic
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("dup groups: %w", err)
	}
	defer rows.Close()
	groups := []DupGroup{}
	for rows.Next() {
		var g DupGroup
		if err := rows.Scan(&g.Topic, &g.IDs); err != nil {
			return nil, fmt.Errorf("dup groups: scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dup groups: rows: %w", err)
	}
	return groups, nil
}

// ConflictGroup — a group for the maintain --conflict-suspects report
// (DupGroup plus per-row summaries. summaries follow the same id-
// ascending order as ids).
type ConflictGroup struct {
	Topic     string
	IDs       []int
	Summaries []string
}

// ConflictGroups returns groups of 2+ active rows sharing a topic
// together with summaries (report only). The grouping formula is the
// same as DupGroups, but it also fetches the summary needed for the
// summary-similarity classification (conflict-suspects) — the existing
// DupGroups signature·output unchanged (a parallel new query).
func (r *Repo) ConflictGroups(ctx context.Context, limit int) ([]ConflictGroup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT topic, array_agg(id ORDER BY id), array_agg(summary ORDER BY id)
		FROM knowledge
		WHERE invalid_at IS NULL AND status = 'active'
		GROUP BY topic HAVING count(*) > 1
		ORDER BY topic
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("conflict groups: %w", err)
	}
	defer rows.Close()
	groups := []ConflictGroup{}
	for rows.Next() {
		var g ConflictGroup
		if err := rows.Scan(&g.Topic, &g.IDs, &g.Summaries); err != nil {
			return nil, fmt.Errorf("conflict groups: scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conflict groups: rows: %w", err)
	}
	return groups, nil
}

// StaleSessionRuns counts the consecutive sessions from the agent's
// most recent with no knowledge write (the done warning verdict). The
// aggregation stops at the first session with a record.
func (r *Repo) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.id,
		        EXISTS (SELECT 1 FROM knowledge k WHERE k.session_id = s.id)
		 FROM sessions s WHERE s.agent = $1
		 ORDER BY s.ts DESC, s.id DESC`, agent)
	if err != nil {
		return 0, fmt.Errorf("stale runs: %w", err)
	}
	defer rows.Close()
	stale := 0
	for rows.Next() {
		var id int
		var hasKnowledge bool
		if err := rows.Scan(&id, &hasKnowledge); err != nil {
			return 0, fmt.Errorf("stale runs: scan: %w", err)
		}
		if hasKnowledge {
			break
		}
		stale++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("stale runs: rows: %w", err)
	}
	return stale, nil
}
