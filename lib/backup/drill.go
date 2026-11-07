// drill.go — T55 restore --drill: 백업 복구 훈련(리스토어 리허설).
// 덤프 SQL을 임시 데이터베이스 flint_drill_db에 재생해 knowledge 행수를
// 대조하고, 종료 시(실패 포함) 데이터베이스를 항상 DROP한다. 원본 public
// 데이터는 불가침.
package backup

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
)

const (
	// DrillDB — 드릴 재생 전용 임시 데이터베이스.
	DrillDB = "flint_drill_db"
	// LastDrillKey — 마지막 드릴 성공 시각(RFC3339) meta 키. maintain --check가 읽는다.
	LastDrillKey = "last_drill"
	// drillTimeout — 드릴 1회(재생) 상한.
	drillTimeout = 120 * time.Second
	// drillCleanupTimeout — 종료 정리 DROP 전용 상한. 재생 ctx(120s)와
	// 분리 — 재생이 상한을 전부 소진해도 정리는 살아 있는 ctx로 실행된다.
	drillCleanupTimeout = 30 * time.Second
)

// DrillResult — 드릴 결과. Expected는 덤프 내 knowledge INSERT 문 수,
// Restored는 flint_drill_db에 실제 재생된 knowledge 행수. Ok는 양자 일치.
type DrillResult struct {
	Expected int
	Restored int
	Ok       bool
}

// dump가 생성하는 public 시퀀스 조작문 — 드릴 재생에서는 건너뛴다.
// Dump의 CREATE SEQUENCE IF NOT EXISTS public.x·SELECT setval('public.x',…)을
// 그대로 재생하면 재생 대상 DB의 public 시퀀스가 바뀐다. 드릴 DB는 통째로
// 폐기되므로 사실상 무해하지만, 행수 대조에도 불필요하므로 생략한다.
var (
	drillCreateSeqRe = regexp.MustCompile(`(?i)^CREATE\s+SEQUENCE\s+IF\s+NOT\s+EXISTS\s+public\.`)
	drillSetvalRe    = regexp.MustCompile(`(?i)^SELECT\s+setval\s*\(\s*'public\.`)
)

// skipForDrill — 드릴 재생에서 제외할 문장(public 시퀀스 조작문) 판정.
func skipForDrill(stmt string) bool {
	s := stripComments(stmt)
	return drillCreateSeqRe.MatchString(s) || drillSetvalRe.MatchString(s)
}

// countInserts — 문장 목록에서 table INTO 문 수를 센다(대소문자·따옴표 무관).
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

// RunDrill — dumpPath 덤프를 임시 데이터베이스 flint_drill_db에 재생해
// knowledge 행수를 대조한다. 재생은 원본 DSN과 분리된 드릴 DB 전용 풀에서
// 일어나므로 원본 풀의 어떤 커넥션 상태(search_path 등)도 오염되지 않는다.
// 성공 시 maintenance_runs 1행(kind=drill)과 meta last_drill을 원본 DSN에
// 기록한다. 드릴 DB는 성공·실패 무관하게 항상 DROP한다(40001은 재시도로
// 회복). 행수 불일치는 에러가 아닌 Ok=false로 보고한다. DROP 재시도 소진
// 시 잔존을 숨기지 않고 성공 대신 명시적 실패를 반환한다(계약: 재생 실패
// 시에도 잔존 금지 — CI run 39719705178). DROP이 nil을 반환해도 pg_database
// 로 잔존을 검증한다(무음 no-op 엔진 결함 방어 — v0.16.2). 테이블·스키마
// 개별 DROP이 무음 no-op로 잔존하는 doltgresql 결함(1.3.3·1.4.0 실측)과
// 무관하게 DB 단위 삭제는 이 결함 조합에서도 동작한다(실측) — v0.15.2
// 스키마→DB 격리 전환. 종료 정리 DROP은 재생 ctx와 독립된 짧은 타임아웃
// 으로 실행된다(재생이 120s를 소진해도 정리는 항상 실행).
func RunDrill(dsn string, dumpPath string) (res DrillResult, err error) {
	ctx, cancel := drillCtxFn()
	defer cancel()

	data, err := os.ReadFile(dumpPath)
	if err != nil {
		return DrillResult{}, err
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return DrillResult{}, err
	}
	defer pool.Close()

	drop := func(ctx context.Context) error {
		return db.Retry(ctx, func() error { return dropDrillDBFn(ctx, pool) }, drillDropBackoff)
	}
	if derr := drop(ctx); derr != nil { // 선행 잔존 정리(이전 비정상 종료 대비)
		return DrillResult{}, fmt.Errorf("drill: 선행 %s 정리 실패: %w", DrillDB, derr)
	}
	defer func() {
		// 종료 정리 DROP은 재생 ctx와 독립된 별도 타임아웃(v0.16.2) —
		// 재생이 상한을 전부 소진해도 정리는 살아 있는 ctx로 항상 실행된다.
		cctx, ccancel := context.WithTimeout(context.Background(), drillCleanupTimeout)
		defer ccancel()
		if derr := drop(cctx); derr != nil && err == nil {
			err = fmt.Errorf("drill: %s 제거 재시도 소진(잔존): %w", DrillDB, derr)
		}
	}()

	if _, err := pool.Exec(ctx, `CREATE DATABASE `+DrillDB); err != nil {
		return DrillResult{}, fmt.Errorf("drill: create database: %w", err)
	}

	// 드릴 DB 접속 — 원본 DSN 설정(자격증명·호스트)을 승계하고 대상 DB만
	// 바꾼다. 재생은 이 별도 풀에서만 일어난다.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: 드릴 DSN 조립: %w", err)
	}
	cfg.ConnConfig.Database = DrillDB
	drillPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return DrillResult{}, fmt.Errorf("drill: 드릴 DB 접속: %w", err)
	}

	// 재생 — 덤프는 BEGIN/COMMIT을 포함하므로 트랜잭션 상태가 커넥션에
	// 남는다: 전체를 1커넥션에서 실행한다. 커넥션·풀은 모든 경로에서
	// 반납·종료한다 — DROP DATABASE는 드릴 DB 커넥션이 닫혀야 성공한다.
	var expected, restored int
	replayErr := func() error {
		defer drillPool.Close()
		conn, err := drillPool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("drill: 재생 커넥션: %w", err)
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
			return fmt.Errorf("drill: knowledge 행수: %w", err)
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

	// 성공 기록 — 원본 DSN 공용 스키마에 명시 한정 기록. 개발 DB가 v0.4
	// 이전 생성이면 maintenance_runs이 없을 수 있다(마이그레이션 게이트
	// 공백) — 드릴 성공이 기록 실패로 뭉개지지 않게 테이블을 보장한다.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		return res, fmt.Errorf("drill: maintenance_runs 보장: %w", err)
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

// drillDropBackoff — 드릴 DB DROP의 40001(직렬화 실패) 재시도 간격.
// CI 직렬화 부하에서 DROP이 타 세션 커밋과 충돌해 실패했는데 오류가
// 무시되면 DB가 잔존했다(CI run 39719705178) — 승격 커밋·views 재적용과
// 같은 회복 패턴으로 짧게 3회 시도한다.
var drillDropBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// dropDrillDBFn — DROP 주입 seam(회귀 테스트가 일시 실패를 흉내 낸다).
var dropDrillDBFn = dropDrillDB

// drillCtxFn — 재생 컨텍스트 조립 seam(단위 테스트가 만료를 주입한다).
var drillCtxFn = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), drillTimeout)
}

// drillResidueFn — pg_database 잔존 조사 seam(단위 테스트가 잔존을 주입).
var drillResidueFn = drillDBResidue

// dropDrillDB — flint_drill_db 데이터베이스 제거+잔존 검증. 테이블·스키마
// 개별 DROP 대신 DB 단위로 지운다 — doltgresql의 무음 DROP no-op 결함이
// 재생 테이블 잔존으로 번지지 않는다(실측). DROP이 nil을 반환해도 믿지
// 않는다: no-op 엔진 결함은 에러 없이 잔존시킨다. pg_database로 직접
// 확인해 잔존을 숨기지 않고 명시적 실패로 돌려준다(v0.16.2 계약 회복).
func dropDrillDB(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+DrillDB); err != nil {
		return err
	}
	n, err := drillResidueFn(ctx, pool)
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("drill: %s 잔존 — 수동 DROP 필요", DrillDB)
	}
	return nil
}

// drillDBResidue — pg_database의 flint_drill_db 행수(0=부재).
func drillDBResidue(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
