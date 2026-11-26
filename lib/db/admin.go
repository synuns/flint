package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("flint: not found")

type Counts struct {
	Pending    int
	DupSuspect int
}

var gateRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 9 * time.Second}

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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 11 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v11: begin: %w", err)
	}
	defer tx.Rollback(ctx)

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
		return nil
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

	var hasScore int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'knowledge' AND column_name = 'recall_score'`).Scan(&hasScore); err != nil {
		return fmt.Errorf("schema v12: check recall_score: %w", err)
	}
	if hasScore == 1 {

		if _, err := tx.Exec(ctx,
			`INSERT INTO recall_events (knowledge_id, recalled_at)
			 SELECT k.id, COALESCE(k.last_recalled_at, now())
			 FROM knowledge k, generate_series(1, k.recall_score) g
			 WHERE k.recall_score > 0`); err != nil {
			return fmt.Errorf("schema v12: backfill: %w", err)
		}

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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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
		return nil
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

func (r *Repo) EnsureSchemaV19(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV19(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV19(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v19: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 19 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v19: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS kind text`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS source_path text`,
		`CREATE TABLE IF NOT EXISTS echo_candidates (
		    id           bigserial PRIMARY KEY,
		    ts           timestamptz NOT NULL DEFAULT now(),
		    candidate_id int NOT NULL,
		    echo_id      int NOT NULL,
		    relation     text NOT NULL,
		    score        double precision NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_echo_candidates_candidate_id
		 ON echo_candidates (candidate_id)`,
		`CREATE INDEX IF NOT EXISTS idx_echo_candidates_ts
		 ON echo_candidates (ts)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v19: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '19')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v19: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v19: commit: %w", err)
	}
	return nil
}

func (r *Repo) EnsureSchemaV20(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV20(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV20(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v20: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 20 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v20: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS claim text NOT NULL DEFAULT ''`,
		`UPDATE knowledge SET claim = 'event' WHERE claim = ''`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_claim_idx ON knowledge (claim)`,
		`CREATE TABLE IF NOT EXISTS work_state (
		    id           serial PRIMARY KEY,
		    project_id   integer REFERENCES projects(id),
		    agent        text NOT NULL DEFAULT 'claude',
		    status       text NOT NULL DEFAULT 'open',
		    task         text NOT NULL,
		    detail       text NOT NULL DEFAULT '',
		    next_action  text NOT NULL DEFAULT '',
		    state_hash   text NOT NULL,
		    session_id   integer REFERENCES sessions(id),
		    opened_at    timestamptz NOT NULL DEFAULT now(),
		    updated_at   timestamptz NOT NULL DEFAULT now(),
		    resolved_at  timestamptz,
		    resolved_by  integer
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS flint_work_state_open_idx
		 ON work_state (project_id, state_hash) WHERE status = 'open'`,
		`CREATE OR REPLACE VIEW recall_knowledge AS
		 SELECT id, topic, summary, tags, entity, domain, claim, confidence, conditions, status
		 FROM knowledge
		 WHERE invalid_at IS NULL AND claim <> 'event'`,
		`CREATE OR REPLACE VIEW open_work_state AS
		 SELECT id, project_id, task, detail, next_action, updated_at
		 FROM work_state WHERE status = 'open'`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v20: create: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '20')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v20: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v20: commit: %w", err)
	}
	return nil
}

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

func (r *Repo) Merge(ctx context.Context, fromID, toID int) error {
	exec, query, unlock, err := supersedeLockFn(ctx, r)
	if err != nil {
		return err
	}

	defer unlock()
	err = r.mergeUnderSupersedeLock(ctx, exec, query, fromID, toID)

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

type DupGroup struct {
	Topic string
	IDs   []int
}

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

type ConflictGroup struct {
	Topic     string
	IDs       []int
	Summaries []string
}

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
