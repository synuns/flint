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

var newStore = func(ctx context.Context, dsn string) (Store, error) {
	return newRealStore(ctx, dsn)
}

func Run(args []string, stdin io.Reader, stdout io.Writer) int {
	return wire.Run(args, stdin, stdout, newStore)
}

func init() {

	wire.ConfigDir = configDir

	Register("write", runWrite)
	Register("read", runRead)
	Register("hot", runHot)
	Register("backup", ops.RunBackup)
	Register("restore", ops.RunRestore)

	knowledge.ParseAsOfSpecFn = wire.ParseAsOfSpec
	knowledge.ErrInvalidAsOf = wire.ErrInvalidAsOf
	knowledge.SessionLabelFn = sessionLabel
	knowledge.ExposureProfileFn = ExposureProfile
	knowledge.RecordHotLenFn = RecordHotLen
	knowledge.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	knowledge.PersistHotStatsToMetaFn = persistHotStatsToMeta

	knowledge.PersistScanStatsFn = ops.PersistScanStatsToMeta
	knowledge.WriteTypedMarkerFn = WriteTypedMarker
	knowledge.BackupKeyPathFn = BackupKeyPath

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

	ops.UpdateCacheInfoFn = updateCacheInfo
	ops.FetchRegistryVersionFn = fetchRegistryVersion

	ops.RegistryCacheInfoFn = registryCacheInfo
	ops.PctOfFn = pctOf

	ops.ComposePsFn = func(ctx context.Context) ([]byte, error) { return composePsFn(ctx) }

	ops.FlintBinPathFn = func() string { return flintBinPathFn() }

	ops.MaintainRetryDelaysFn = func() []time.Duration { return maintainRetryDelays }

	ops.ExposureProfileFn = func(getKV func(string) (string, error)) string { return exposureProfileFn(getKV) }

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

	ops.DumpFn = func(ctx context.Context, dsn string, w io.Writer) error { return dumpFn(ctx, dsn, w) }
	ops.RestoreFn = func(ctx context.Context, dsn string, r io.Reader) error { return restoreFn(ctx, dsn, r) }
	ops.RotateFn = func(dir string, keep int) ([]string, error) { return rotateFn(dir, keep) }
	ops.KeepFn = func(getKV func(string) (string, error)) int { return keepFn(getKV) }
}

type realStore struct {
	repo *db.Repo
	pool *pgxpool.Pool
}

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

	latest, err := ensureSchemas(ctx, repo, s)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}

	if !latest {
		if _, err := ensureSchemas(ctx, repo, s); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func isPgUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

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

var _ wire.Store = (*realStore)(nil)
