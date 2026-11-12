// Package cli — flint 커맨드 진입점. os.Args 수동 파싱(프롬프트·TUI 금지),
// 출력은 한 줄 JSON 또는 고정 포맷, exit code 0/1/2/3 계약을 지킨다.
// v0.38.0 §1: 디스패치·계약은 lib/wire로 추출. v0.40.0 T4: 이 파일은
// composition root — realStore 어댑터·cli.Run 위임 래퍼와 함께 커맨드
// 등록·양방향 seam 주입(ops·knowledge가 cli/wire 실구현을 쓰는 주입점)을
// 이 init 한 곳에서 소유한다(별칭 파일의 산재 init 수렴).
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// newStore — seam. 기본 실구현, 테스트는 fake으로 교체(SUPERLITE_REAL=1은 실구현 위임).
var newStore = func(ctx context.Context, dsn string) (Store, error) {
	return newRealStore(ctx, dsn)
}

// Run은 wire.Run 위임 래퍼 — cli.newStore seam을 공장으로 주입한다
// (테스트 교체 관례 불변: 호출 시점의 newStore var 값을 전달).
func Run(args []string, stdin io.Reader, stdout io.Writer) int {
	return wire.Run(args, stdin, stdout, newStore)
}

// init — composition root(v0.40.0 T4 수렴). ① wire 설정 주입 ② cli 소유
// 핵심 커맨드 등록 ③ ops·knowledge 주입 seam(공용 유틸은 wire 직접,
// cli 잔류 실구현은 별칭 경유). 교체 전파가 필요한 seam(composePsFn·
// exposureProfileFn·backup 4종)은 cli var를 호출 시점에 읽는 래퍼로
// 주입한다 — 테스트의 cli var 재할당이 ops 실행 경로에 전파된다.
// doctor·status·tuning·maintain·nudge·session-*·report 등록은 ops
// 패키지 init이, write·read·hot·approve·pending·brief·done·compact는
// knowledge 패키지 init이 담당한다(이 init이 knowledge·ops를 임포트해
// 활성화).
func init() {
	// ① wire 계약층 주입 — 설정 디렉터리 규칙(FLINT_CONFIG_DIR >
	// os.UserConfigDir()/flint)은 cli(marker.go→ops 이동분) 소유.
	wire.ConfigDir = configDir

	// ② cli 소유 커맨드 등록 — knowledge 이동분 핵심 3종 + ops 이동분
	// backup·restore(핸들러 직접 등록). help는 미등록 — wire.Run이
	// store 접속 전에 출력하는 usage 경로가 담당한다.
	Register("write", runWrite)
	Register("read", runRead)
	Register("hot", runHot)
	Register("backup", ops.RunBackup)
	Register("restore", ops.RunRestore)

	// ③-1 knowledge 주입 seam — as-of 정규화는 wire 이동분 직접,
	// 마커·계측 계열은 ops 이동분 별칭 경유.
	knowledge.ParseAsOfSpecFn = wire.ParseAsOfSpec
	knowledge.ErrInvalidAsOf = wire.ErrInvalidAsOf
	knowledge.SessionLabelFn = sessionLabel
	knowledge.ExposureProfileFn = ExposureProfile
	knowledge.RecordHotLenFn = RecordHotLen
	knowledge.LoadHotStatsFromMetaFn = loadHotStatsFromMeta
	knowledge.PersistHotStatsToMetaFn = persistHotStatsToMeta
	knowledge.WriteTypedMarkerFn = WriteTypedMarker
	knowledge.BackupKeyPathFn = BackupKeyPath

	// ③-2 ops 주입 seam — cli 잔류 실구현(version·update·backup 인프라).
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
	ops.PctOfFn = pctOf

	// ③-3 교체 전파 래퍼 — cli var 재할당을 ops가 관측(composePsFn 패턴).
	// v0.43.0 T3 조사: 유지분은 각 1줄 사유(소비 cli 테스트가 var 교체로
	// ops 실행 경로를 검증 — 래퍼 제거 시 전파 단절). 교체 소비 테스트가
	// 없는 windowedDaily는 직접 참조로 전환했다(아래).
	// 유지 — doctor_v07·rules_sync_v097_test가 composePsFn 교체 후 runDoctor 검증.
	ops.ComposePsFn = func(ctx context.Context) ([]byte, error) { return composePsFn(ctx) }
	// 유지 — doctor_v094·rules_sync_v097_test가 flintBinPathFn 교체 후 runDoctor 검증.
	ops.FlintBinPathFn = func() string { return flintBinPathFn() }
	// 유지 — maintain_v08_test가 maintainRetryDelays 교체 후 runMaintain 검증.
	ops.MaintainRetryDelaysFn = func() []time.Duration { return maintainRetryDelays }
	// 유지 — nudge_v0110_test가 exposureProfileFn 교체 후 runNudge 검증.
	ops.ExposureProfileFn = func(getKV func(string) (string, error)) string { return exposureProfileFn(getKV) }
	// WindowedDailyFn — cli var 교체 소비 테스트 없음: ops 실구현 직접
	// 참조(ReportDay→DailyRow 변환 어댑터만 — v0.43.0 T3 전환).
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
	// 유지 — backup_test·wire_approve_store_test가 4종 교체 후 runBackup·runRestore 검증.
	ops.DumpFn = func(ctx context.Context, dsn string, w io.Writer) error { return dumpFn(ctx, dsn, w) }
	ops.RestoreFn = func(ctx context.Context, dsn string, r io.Reader) error { return restoreFn(ctx, dsn, r) }
	ops.RotateFn = func(dir string, keep int) ([]string, error) { return rotateFn(dir, keep) }
	ops.KeepFn = func(getKV func(string) (string, error)) int { return keepFn(getKV) }
}

// realStore — 쓰기는 db.Repo, 읽기·ping은 풀로 위임하는 어댑터.
type realStore struct {
	repo *db.Repo
	pool *pgxpool.Pool
}

// newRealStore — 접속 후 스키마를 보장한다(Migrate는 멱등).
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
	// 컬럼이 뷰보다 먼저: 승격 게이트(v2~v4)를 Migrate의 뷰 재적용 앞에
	// 돌린다. v4 뷰(views.sql)가 knowledge.read_count·last_read_at을
	// 참조하므로, 레거시 v3 DB에서 컬럼 없이 뷰부터 만들면 CREATE VIEW가
	// 42703으로 실패해 재시도를 소진하고(전 커맨드 실패) 뷰 DROP만 남아
	// 구형 CLI도 파손한다. 신규 DB(테이블 부재)는 게이트가 no-op로 지나친다.
	latest, err := ensureSchemas(ctx, repo, s)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// 2차 게이트 — 신규 DB는 Migrate가 schema_version=1로 테이블을 만들므로
	// 여기서 v2→최신으로 자연 승격된다(기존 배포 경로 동일). F6(성능):
	// 진입 probe(ensureSchemas)가 최신을 확인했으면 스킵 — Migrate는
	// 버전을 내리지 않아 승격 원천이 없고, 뷰 최신성은 Migrate의
	// views_hash 게이트가 계속 담당한다.
	if !latest {
		if _, err := ensureSchemas(ctx, repo, s); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// schemaVersionFn — ensureSchemas 최신 단축의 schema_version 1회 조회
// seam(F6 성능). ok=false는 조회 실패·meta 부재·미기록·비정수 — 기존
// 순차 승격 경로(게이트 각자의 오류 처리)가 담당한다. 테스트는 호출
// 카운트로 순회 스킵을 단언한다.
var schemaVersionFn = func(ctx context.Context, st Store) (version int, ok bool) {
	rows, err := st.Query(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0, false
	}
	n, perr := strconv.Atoi(fmt.Sprint(rows[0][0]))
	if perr != nil {
		return 0, false
	}
	return n, true
}

// ensureSchemas — 등록표(db.SchemaGates) 순차 승격(newRealStore 전용).
// 순서 불변: v1 DB가 최신 버전으로 건너뛰며 중간 컬럼·인덱스·테이블을
// 놓치지 않게 한다. 게이트 목록·순서는 등록표 단일 소스(연속성은
// TestSchemaGatesContiguous·파생은 TestEnsureSchemasUsesGates 가드).
// F6(성능) 최신 단축: 진입 시 schema_version 1회 조회(schemaVersionFn)
// 로 최신(LatestSchemaVersion) DB는 순회를 통째로 건너뛴다 — 게이트
// 8종이 각자 버전을 재조회하던 접속당 16왕복(2회 호출×8)을 probe 1회로.
// 조회 실패·레거시·신규 DB는 기존 순차 승격 경로 유지. 반환값 latest는
// 진입 probe가 최신을 확인했는지(newRealStore 2차 게이트 스킵 판정).
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

// Close — 풀 정리.
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
