// Package cli — flint command entry points. Manual os.Args parsing (no prompts, no TUI),
// output is one-line JSON or a fixed format, honoring the exit code 0/1/2/3 contract.
// Dispatch and contracts were extracted to lib/wire. This file is the
// composition root — it owns command registration together with the realStore adapter
// and the cli.Run delegation wrapper, plus bidirectional seam injection (the injection point where ops and knowledge
// use the cli/wire real implementations) in this single init (converging the scattered inits of the alias files).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// newStore — seam. Real implementation by default; tests swap in a fake (SUPERLITE_REAL=1 delegates to the real implementation).
var newStore = func(ctx context.Context, dsn string) (Store, error) {
	return newRealStore(ctx, dsn)
}

// Run is a wire.Run delegation wrapper — injects the cli.newStore seam as the factory
// (the test-swap convention unchanged: passes the newStore var value at call time).
func Run(args []string, stdin io.Reader, stdout io.Writer) int {
	return wire.Run(args, stdin, stdout, newStore)
}

// init — composition root. (1) inject wire configuration (2) register cli-owned
// core commands (3) ops and knowledge injection seams (shared utils go directly to wire;
// cli-residual real implementations go via aliases). Seams that need swap propagation (composePsFn,
// exposureProfileFn, the 4 backup seams) are injected as wrappers reading the cli vars at call time
// — test reassignment of cli vars propagates to the ops execution path.
// Registration of doctor, status, tuning, maintain, nudge, session-*, and report is handled by the ops
// package init; write, read, hot, approve, pending, brief, done, and compact by the
// knowledge package init (this init imports knowledge and ops to
// activate them).
func init() {
	// (1) wire contract-layer injection — the config-directory rule (FLINT_CONFIG_DIR >
	// os.UserConfigDir()/flint) is cli-owned (marker.go -> moved to ops).
	wire.ConfigDir = configDir

	// (2) cli-owned command registration — the 3 core commands moved to knowledge + backup and restore
	// moved to ops (handlers registered directly). help is unregistered — the usage path that
	// wire.Run prints before connecting to the store covers it.
	Register("write", runWrite)
	Register("read", runRead)
	Register("hot", runHot)
	Register("backup", ops.RunBackup)
	Register("restore", ops.RunRestore)

	// (3)-1 knowledge injection seams — as-of normalization goes directly to the wire version;
	// the marker and metering families go via the ops-moved aliases.
	knowledge.ParseAsOfSpecFn = wire.ParseAsOfSpec
	knowledge.ErrInvalidAsOf = wire.ErrInvalidAsOf
	knowledge.SessionLabelFn = sessionLabel
	knowledge.ExposureProfileFn = ExposureProfile
	knowledge.RecordHotLenFn = RecordHotLen
	knowledge.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	knowledge.PersistHotStatsToMetaFn = persistHotStatsToMeta
	// v1.21.1 A6 — the write/read exit scan_stats flush (one-shot processes).
	knowledge.PersistScanStatsFn = ops.PersistScanStatsToMeta
	knowledge.WriteTypedMarkerFn = WriteTypedMarker
	knowledge.BackupKeyPathFn = BackupKeyPath

	// (3)-2 ops injection seams — cli-residual real implementations (version, update, backup infrastructure).
	ops.CurrentVersionFn = CurrentVersion
	ops.FetchLatestVersionFn = fetchLatestVersion
	ops.ValidVersionFn = validVersion
	ops.CompareVersionsFn = compareVersions
	ops.KnownMetaKeyFn = knownMetaKey
	ops.AnyToTimeFn = anyToTime
	ops.IsConflictGroupFn = isConflictGroup
	ops.ConflictDupThresholdFn = func() float64 { return conflictDupThreshold }
	ops.BackupEncryptOnFn = backupEncryptOn
	ops.BackupKeyPathFn = BackupKeyPath
	ops.SkillSrcDirFn = skillSrcDir
	ops.CheckSkillFileFn = checkSkillFile
	ops.ParseAsOfSpecFn = wire.ParseAsOfSpec
	ops.ErrInvalidAsOf = wire.ErrInvalidAsOf
	ops.ReadZeroPctFn = readZeroPct
	ops.HotStatsFn = HotStats
	ops.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	ops.LoadReadStatsFromMetaFn = loadReadStatsFromMeta
	ops.QuoteJSONFn = quoteJSON
	ops.MaybeUpdateNoticeFn = maybeUpdateNotice
	ops.UpdateNoticeTTL = updateNoticeTTL
	// v1.26.0 T3 — the doctor versions display and the report --text checked_at line.
	ops.UpdateCacheInfoFn = updateCacheInfo
	ops.FetchRegistryVersionFn = fetchRegistryVersion
	// v1.26.1 — the offline fallback display of the same check.
	ops.RegistryCacheInfoFn = registryCacheInfo
	ops.PctOfFn = pctOf

	// (3)-3 swap-propagation wrappers — ops observes cli var reassignment (the composePsFn pattern).
	// Kept seams carry a one-line rationale each (consuming cli tests verify the ops
	// execution path by swapping the var — removing the wrapper breaks propagation). windowedDaily,
	// which has no swap-consuming test, was switched to a direct reference (below).
	// kept — doctor_v07 and rules_sync_v097_test verify runDoctor after swapping composePsFn.
	ops.ComposePsFn = func(ctx context.Context) ([]byte, error) { return composePsFn(ctx) }
	// kept — doctor_v094 and rules_sync_v097_test verify runDoctor after swapping flintBinPathFn.
	ops.FlintBinPathFn = func() string { return flintBinPathFn() }
	// kept — maintain_v08_test verifies runMaintain after swapping maintainRetryDelays.
	ops.MaintainRetryDelaysFn = func() []time.Duration { return maintainRetryDelays }
	// kept — nudge_v0110_test verifies runNudge after swapping exposureProfileFn.
	ops.ExposureProfileFn = func(getKV func(string) (string, error)) string { return exposureProfileFn(getKV) }
	// WindowedDailyFn — no cli var swap-consuming test: direct reference to the ops real
	// implementation (only the ReportDay->DailyRow conversion adapter).
	ops.WindowedDailyFn = func(ctx context.Context, st wire.Store, days int) ([]ops.DailyRow, error) {
		rows, err := ops.WindowedDaily(ctx, st, days)
		if err != nil {
			return nil, err
		}
		out := make([]ops.DailyRow, 0, len(rows))
		for _, d := range rows {
			out = append(out, ops.DailyRow{Read: d.Read, ReadZero: d.ReadZero, ReadNear: d.ReadNear})
		}
		return out, nil
	}
	// kept — backup_test and wire_approve_store_test verify runBackup/runRestore after swapping the 4 seams.
	ops.DumpFn = func(ctx context.Context, dsn string, w io.Writer) error { return dumpFn(ctx, dsn, w) }
	ops.RestoreFn = func(ctx context.Context, dsn string, r io.Reader) error { return restoreFn(ctx, dsn, r) }
	ops.RotateFn = func(dir string, keep int) ([]string, error) { return rotateFn(dir, keep) }
	ops.KeepFn = func(getKV func(string) (string, error)) int { return keepFn(getKV) }
}

// realStore — adapter delegating writes to db.Repo and reads/pings to the pool.
type realStore struct {
	repo *db.Repo
	pool *pgxpool.Pool
}

// newRealStore — ensures the schema after connecting (Migrate is idempotent).
func newRealStore(ctx context.Context, dsn string) (Store, error) {
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		return nil, err
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		return nil, err
	}
	s := &realStore{repo: repo, pool: pool}
	// columns before views: run the promotion gates (v2-v4) ahead of Migrate's view reapplication.
	// Since the v4 views (views.sql) reference knowledge.read_count and last_read_at,
	// creating the views first without the columns on a legacy v3 DB makes CREATE VIEW fail with
	// 42703, exhausting retries (every command fails) and leaving only the views dropped,
	// breaking old CLIs too. New DBs (no tables) pass the gate as a no-op.
	latest, err := ensureSchemas(ctx, repo, s)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// second gate — a new DB gets its tables from Migrate at schema_version=1, so it is
	// naturally promoted v2->latest here (same as the existing deployment path). F6 (performance):
	// skip if the entry probe (ensureSchemas) already confirmed latest — Migrate never
	// lowers the version, so there is no promotion source, and view freshness remains the
	// responsibility of Migrate's views_hash gate.
	if !latest {
		if _, err := ensureSchemas(ctx, repo, s); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// isPgUndefinedTable — detects 42P01 (undefined table). db.isPgCode is unexported, so
// a minimal cli-side copy stays here (same logic — errors.As PgError.Code comparison).
func isPgUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// schemaVersionFn — seam doing the single schema_version lookup for the ensureSchemas
// latest shortcut (F6 performance). ok=false covers lookup failure, missing meta, unrecorded,
// or non-integer — the existing sequential promotion path (each gate's own error handling)
// takes over. Tests assert the walk is skipped via call counts.
// v1.7.2 T4 (L1): if the probe fails with 42P01, the session catalog may be stale
// (R38-B probeheal — a Doltgres observation where the first query reports
// undefined table even though the table exists) — run SELECT 1 once on the st pool
// to resynchronize the catalog, then retry the probe once. If it is still 42P01 on retry,
// it is a genuinely new DB, so fall back to the existing sequential promotion path (at most
// +2 round trips per new-DB connection).
var schemaVersionFn = func(ctx context.Context, st Store) (version int, ok bool) {
	rows, err := st.Query(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`)
	if err != nil {
		if isPgUndefinedTable(err) {
			if _, herr := st.Query(ctx, `SELECT 1`); herr == nil {
				rows, err = st.Query(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`)
			}
		}
		if err != nil {
			return 0, false
		}
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, false
	}
	n, perr := strconv.Atoi(fmt.Sprint(rows[0][0]))
	if perr != nil {
		return 0, false
	}
	return n, true
}

// ensureSchemas — sequential promotion through the registry (db.SchemaGates), for newRealStore only.
// Order invariant: lets a v1 DB jump to the latest version without missing intermediate
// columns, indexes, or tables. The gate list and order are the single registry source (continuity is
// guarded by TestSchemaGatesContiguous, derivation by TestEnsureSchemasUsesGates).
// F6 (performance) latest shortcut: one schema_version lookup on entry (schemaVersionFn)
// lets a latest (LatestSchemaVersion) DB skip the whole walk — instead of 16 round trips per
// connection (2 calls x 8 gates each re-querying the version), one probe. Lookup failure,
// legacy, or new DBs keep the existing sequential promotion path. The latest return value
// tells whether the entry probe confirmed latest (used to skip the newRealStore second gate).
func ensureSchemas(ctx context.Context, repo *db.Repo, st Store) (latest bool, err error) {
	if v, ok := schemaVersionFn(ctx, st); ok && v >= db.LatestSchemaVersion {
		return true, nil
	}
	for _, g := range db.SchemaGates() {
		if err := g.Ensure(ctx, repo); err != nil {
			return false, err
		}
	}
	return false, nil
}

// Close — cleans up the pool.
func (s *realStore) Close() {
	s.repo.Close()
	s.pool.Close()
}

func (s *realStore) Migrate(ctx context.Context) error { return s.repo.Migrate(ctx) }

func (s *realStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return s.repo.InsertBatch(ctx, recs)
}

func (s *realStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return s.repo.ExistsHash(ctx, hash)
}

func (s *realStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return s.repo.ExistsProject(ctx, name)
}

func (s *realStore) Commit(ctx context.Context, msg string) (string, error) {
	return s.repo.Commit(ctx, msg)
}

func (s *realStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// InsertKnowledgeLink — the knowledge.LinkAccrualStore extension
// (v1.20.0 T5): the idempotent knowledge_links INSERT — a resend of the
// same (from,to,relation) skips (the seeding idempotence convention).
// v1.20.1 (r86): the guard is ON CONFLICT DO NOTHING over the schema v15
// unique index uq_knowledge_links_triple — the former NOT EXISTS form
// left a check-then-insert window that let parallel writers accrue the
// same link twice. The write SQL literal lives in this store layer (the
// lib/{ops,knowledge} write-guard scope owns write verbs here).
func (s *realStore) InsertKnowledgeLink(ctx context.Context, fromID, toID int, relation string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (from_id, to_id, relation) DO NOTHING`,
		fromID, toID, relation)
	return err
}

func (s *realStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// --- history-contract delegation (db.Repo is the sole dialect owner) ---

func (s *realStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	return s.repo.LogEntries(ctx, q)
}

func (s *realStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return s.repo.QueryAsOf(ctx, q)
}

func (s *realStore) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return s.repo.DiffRows(ctx, fromHash, toHash)
}

func (s *realStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return s.repo.DiffStat(ctx, hashes)
}

func (s *realStore) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return s.repo.DiffCommits(ctx, fromHash, toHash)
}

// compile-time guarantee — realStore satisfies all of wire.Store (the R33 convention).
var _ wire.Store = (*realStore)(nil)
