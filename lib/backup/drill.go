// drill.go — T55 restore --drill: 백업 복구 훈련(리스토어 리허설).
// 덤프 SQL을 임시 스키마 flint_drill에 재생해 knowledge 행수를 대조하고,
// 종료 시(실패 포함) 스키마를 항상 DROP한다. 원본 public 데이터는 불가침.
package backup

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/db"
)

const (
	// DrillSchema — 드릴 재생 전용 임시 스키마.
	DrillSchema = "flint_drill"
	// LastDrillKey — 마지막 드릴 성공 시각(RFC3339) meta 키. maintain --check가 읽는다.
	LastDrillKey = "last_drill"
	// drillTimeout — 드릴 1회 상한.
	drillTimeout = 120 * time.Second
)

// DrillResult — 드릴 결과. Expected는 덤프 내 knowledge INSERT 문 수,
// Restored는 flint_drill에 실제 재생된 knowledge 행수. Ok는 양자 일치.
type DrillResult struct {
	Expected int
	Restored int
	Ok       bool
}

// dump가 생성하는 public 시퀀스 조작문 — 드릴 재생에서는 건너뛴다.
// Dump의 CREATE SEQUENCE IF NOT EXISTS public.x·SELECT setval('public.x',…)을
// 그대로 재생하면 flint_drill 이 아니라 원본 public 시퀀스가 바뀐다(비간섭 위반.
// 오래된 keep-N 덤프로 훈련하면 시퀀스가 과거 값으로 되감겨 다음 실제 INSERT가
// PK 충돌한다). 행수 대조에는 영향이 없으므로 드릴에서는 생략해도 된다.
var (
	drillCreateSeqRe = regexp.MustCompile(`(?i)^CREATE\s+SEQUENCE\s+IF\s+NOT\s+EXISTS\s+public\.`)
	drillSetvalRe    = regexp.MustCompile(`(?i)^SELECT\s+setval\s*\(\s*'public\.`)
)

// skipForDrill — 드릴 재생에서 제외할 문장(원본 public 조작문) 판정.
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

// RunDrill — dumpPath 덤프를 flint_drill 스키마에 재생(CREATE SCHEMA·
// SET search_path)해 knowledge 행수를 대조한다. 성공 시 maintenance_runs
// 1행(kind=drill)과 meta last_drill을 기록한다. 스키마는 성공·실패 무관하게
// 항상 DROP한다. 행수 불일치는 에러가 아닌 Ok=false로 보고한다.
func RunDrill(dsn string, dumpPath string) (DrillResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), drillTimeout)
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

	drop := func() { _ = dropDrillSchema(ctx, pool) }
	drop() // 선행 잔존 정리(이전 비정상 종료 대비)
	defer drop()

	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+DrillSchema); err != nil {
		return DrillResult{}, fmt.Errorf("drill: create schema: %w", err)
	}

	var expected int

	// SET search_path는 커넥션 단위 — 풀에서 1커넥션을 잡아 재생 전체에 사용.
	// 반납 전에 반드시 public으로 되돌린다: 잔류한 flint_drill search_path로
	// 풀에 돌아간 커넥션에서 이후 비한정 쿼리가 public 테이블을 못 찾는다
	// (42P01). 드릴 성공 기록(public.maintenance_runs·public.meta)은 재생
	// 커넥션과 분리된 풀 쿼리로 실행한다.
	var restored int
	err = drillConn(ctx, pool, func(conn *pgxpool.Conn) error {
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
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM `+DrillSchema+`.knowledge`).Scan(&restored); err != nil {
			return fmt.Errorf("drill: knowledge 행수: %w", err)
		}
		return nil
	})
	if err != nil {
		return DrillResult{Expected: expected}, err
	}
	res := DrillResult{Expected: expected, Restored: restored, Ok: expected == restored}
	if !res.Ok {
		return res, nil
	}

	// 성공 기록 — 재생 커넥션과 분리해 공용 스키마에 명시 한정 기록.
	// 개발 DB가 v0.4 이전 생성이면 maintenance_runs이 없을 수 있다(마이그레이션
	// 게이트 공백) — 드릴 성공이 기록 실패로 뭉개지지 않게 테이블을 보장한다.
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

// drillConn — 풀에서 1커넥션을 잡아 search_path를 flint_drill로 바꾼 뒤
// fn을 실행하고, 반환 전(에러 포함) 반드시 public으로 되돌려 반납한다.
// SET search_path는 커넥션 단위 상태라 잔류하면 그 커넥션을 이후 얻는
// 쿼리가 public 테이블을 flint_drill에서 찾다 실패한다(42P01).
func drillConn(ctx context.Context, pool *pgxpool.Pool, fn func(conn *pgxpool.Conn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(ctx, `SET search_path TO public`) // 잔류 방지(최선)
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, `SET search_path = `+DrillSchema); err != nil {
		return fmt.Errorf("drill: search_path: %w", err)
	}
	return fn(conn)
}

// dropDrillSchema — flint_drill 스키마 제거. Doltgres 제약 3가지 대응:
//   - CASCADE 미지원(XX000)
//   - 비어 있지 않은 스키마의 DROP을 에러 없이 조용히 무시
//   - DROP TABLE이 FK 참조를 남겨 부모 테이블 제거를 차단(dangling FK)
//
// 따라서 (1) FK 제약 선행 제거 → (2) 테이블 반복 제거 → (3) 스키마 DROP.
func dropDrillSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if err := dropDrillFKs(ctx, pool); err != nil {
		return err
	}
	for pass := 0; ; pass++ {
		tables, err := drillTables(ctx, pool)
		if err != nil {
			return err
		}
		if len(tables) == 0 {
			break
		}
		if pass > 50 {
			return fmt.Errorf("drill: %s 테이블 제거 진행 불가: %v", DrillSchema, tables)
		}
		progress := false
		for _, t := range tables {
			if _, err := pool.Exec(ctx,
				`DROP TABLE IF EXISTS `+DrillSchema+"."+quoteIdent(t)); err == nil {
				progress = true
			}
		}
		if !progress {
			return fmt.Errorf("drill: %s 테이블 제거 실패: %v", DrillSchema, tables)
		}
	}
	_, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+DrillSchema)
	return err
}

// dropDrillFKs — 스키마 내 FK 제약 선행 제거(최선 노력 — 개별 실패는
// 테이블 제거 단계에서 잡힌다).
func dropDrillFKs(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `SELECT constraint_name, table_name
		FROM information_schema.table_constraints
		WHERE table_schema = $1 AND constraint_type = 'FOREIGN KEY'`, DrillSchema)
	if err != nil {
		return err
	}
	type fk struct{ name, table string }
	var fks []fk
	for rows.Next() {
		var c fk
		if err := rows.Scan(&c.name, &c.table); err != nil {
			rows.Close()
			return err
		}
		fks = append(fks, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range fks {
		_, _ = pool.Exec(ctx, `ALTER TABLE `+DrillSchema+"."+quoteIdent(c.table)+
			" DROP CONSTRAINT "+quoteIdent(c.name))
	}
	return nil
}

// drillTables — flint_drill에 남은 테이블 목록.
func drillTables(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables WHERE table_schema = $1`, DrillSchema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
