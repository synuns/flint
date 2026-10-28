// repo.go — flint 저장소 접근 계층(T7). 스키마 마이그레이션·삽입·dolt 커밋.
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

	"flint/lib/sqlfs"
	"flint/lib/store"
)

// sqlfs seam — 테스트가 fake DDL로 교체한다(SUPERLITE_REAL=1이면 실구현 사용).
var (
	schemaSQLFn = sqlfs.SchemaSQL
	viewsSQLFn  = sqlfs.ViewsSQL
)

// reapplyViews seam — 게이트 테스트가 재적용 발생을 센다(F-T50-1).
var reapplyViewsFn = (*Repo).reapplyViews

// kind → 대상 테이블. write는 INSERT 전용이라 각 kind마다 단일 테이블에만 쓴다.
var kindTables = map[string]string{
	"knowledge": "knowledge",
	"session":   "sessions",
	"project":   "projects",
}

// Repo는 Doltgres 풀을 감싼 저장소다.
type Repo struct {
	pool *pgxpool.Pool
	dsn  string // 뷰 재적용용 simple protocol 접속에 필요
}

// NewRepo는 접속 후 Repo를 돌려준다. 실패는 ErrServerDown으로 전파된다.
func NewRepo(ctx context.Context, dsn string) (*Repo, error) {
	p, err := Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Repo{pool: p, dsn: dsn}, nil
}

// Close는 풀을 정리한다.
func (r *Repo) Close() { r.pool.Close() }

// Migrate는 스키마가 없을 때 SchemaSQL을 실행하고 meta.schema_version=1을
// 기록한다. 버전 게이트는 정수 비교다: schema_version>=1이면 테이블이
// 이미 있다고 본다(v2 승격은 admin.go EnsureSchemaV2 소관 — '1' 문자열
// 일치만 검사하던 v0.1 게이트는 v2 DB에서 nil wrap 실패를 냈다).
// 뷰는 schema_version과 무관하게 최신성(views_hash)으로 게이트한다 —
// 버전이 같아도 뷰 정의가 갱신되는 경우(stale active_knowledge)는
// hash 불일치로 재적용되지만, 이미 최신이면 재작성하지 않는다.
func (r *Repo) Migrate(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("migrate: check schema_version: %w", err)
	}
	// meta가 없거나(최초 생성) 값이 정수로 읽히지 않으면 재생성한다.
	tablesCurrent := false
	if err == nil {
		if n, perr := strconv.Atoi(version); perr == nil && n >= 1 {
			tablesCurrent = true
		}
	}
	if !tablesCurrent { // 최초 생성 또는 버전 불일치: 테이블+meta 기록
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
	// 뷰는 테이블과 달리 트랜잭션 밖 simple protocol로 재적용한다.
	// 다만 매 커맨드마다 DROP+CREATE하면 dolt_schemas가 매번 재작성돼
	// 신규 DB의 병렬 write에서 working-set merge 충돌이 났다(F-T50-1).
	// 뷰가 이미 최신이면 스킵한다 — 최신 판정은 ①배포 SQL 지문(views_hash)
	// ②실제 뷰 컬럼 서명(views_sig, 재적용 직후 스냅샷) 이중 게이트.
	// ①은 뷰 정의 갱신 배포를, ②는 외부로 낡아진 뷰(stale active_knowledge,
	// T15 인시던트)를 각각 잡는다. 신규 DB의 동시 첫 실행은 재적용끼리
	// merge 충돌을 낼 수 있어 재시도로 회복한다(루프 안에서 다른 writer가
	// 완료한 재적용을 viewsCurrent로 흡수). 신규 DB 경합은 수 초간 지속될
	// 수 있어 Commit 기본간격(0s/1s/3s)보다 긴 백오프를 쓴다(실측: 기본
	// 4시도로 회복 못 한 사례 존재).
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
			return fmt.Errorf("views: hash/sig 기록: %w", err)
		}
		return nil
	}, []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 9 * time.Second})
}

// viewsCurrent — 배포 SQL 지문과 실제 뷰 서명이 모두 기록된 값과 같으면
// true. 조회 실패(최초 생성·뷰 결측·외부 변조)는 false로 귀결해 재적용으로.
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

// viewsSig — 각 뷰의 실제 컬럼 순서를 "name:col1,col2\n" 형태로 모은다.
// 뷰가 하나라도 없으면 오류(재적용 트리거). SELECT * LIMIT 0의 필드
// 메타데이터로 읽는다 — Doltgres는 information_schema에 뷰 컬럼을
// 노출하지 않는다(T15).
func (r *Repo) viewsSig(ctx context.Context, viewsSQL string) (string, error) {
	names := viewNames(viewsSQL)
	if len(names) == 0 {
		return "", fmt.Errorf("views: SQL에서 뷰 이름 미발견")
	}
	var b strings.Builder
	for _, name := range names {
		rows, err := r.pool.Query(ctx, "SELECT * FROM "+name+" LIMIT 0")
		if err != nil {
			return "", fmt.Errorf("views: %s 조회: %w", name, err)
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

// viewNames — SQL 스크립트에서 CREATE OR REPLACE VIEW 대상 이름 추출.
var viewNameRe = regexp.MustCompile(`(?is)CREATE\s+OR\s+REPLACE\s+VIEW\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

func viewNames(script string) []string {
	matches := viewNameRe.FindAllStringSubmatch(script, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// hashSQL — 뷰 정의 지문(SHA-256 hex).
func hashSQL(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

// reapplyViews는 뷰를 최신 정의로 재생성한다(테이블·데이터는 불가침).
// Doltgres는 확장 프로토콜에서 CREATE OR REPLACE VIEW가 뷰 컬럼 목록을
// 갱신하지 않는 경우가 있어(stale active_knowledge 인시던트), e2e mustDB와
// 동일하게 simple protocol 커넥션에서 DROP VIEW IF EXISTS 후 문장별 CREATE한다.
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

// InsertBatch는 레코드들을 단일 트랜잭션에 삽입한다(all-or-nothing).
// 각 레코드는 "kind" 키로 대상 테이블을 고르고, 나머지 키-값은 컬럼-값이 된다.
// unique 위반(content_hash·project name)은 store.ErrDuplicate로 변환한다.
func (r *Repo) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, mapInsertErr(err)
	}
	defer tx.Rollback(ctx)
	ids := make([]int, 0, len(recs))
	for _, rec := range recs {
		id, err := insertOne(ctx, tx, rec)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapInsertErr(err)
	}
	return ids, nil
}

// insertOne은 한 레코드를 삽입하고 새 id를 돌려준다.
func insertOne(ctx context.Context, tx pgx.Tx, rec map[string]any) (int, error) {
	kind, _ := rec["kind"].(string)
	table, ok := kindTables[kind]
	if !ok {
		return 0, fmt.Errorf("insert: unknown kind %q", kind)
	}
	cols := make([]string, 0, len(rec))
	for k := range rec {
		if k == "kind" {
			continue
		}
		cols = append(cols, k)
	}
	sort.Strings(cols) // SQL 문자열 안정(쿼리 캐시·로그 비교용)
	args := make([]any, len(cols))
	ph := make([]string, len(cols))
	for i, c := range cols {
		args[i] = rec[c]
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	var id int
	if err := tx.QueryRow(ctx, stmt, args...).Scan(&id); err != nil {
		return 0, mapInsertErr(err)
	}
	return id, nil
}

// DupIDByHash는 해당 content_hash가 knowledge에 이미 있으면 기존 행 id와
// dup=true를 돌려준다(dedup 사전 확인 + duplicate 응답 id 노출, R3).
// 없으면 (0, false, nil).
func (r *Repo) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	var id int
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM knowledge WHERE content_hash = $1`, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("dup_id_by_hash: %w", err)
	}
	return id, true, nil
}

// ExistsHash는 DupIDByHash의 하위 호환 래퍼(dup 여부만 — root.go 어댑터).
func (r *Repo) ExistsHash(ctx context.Context, hash string) (bool, error) {
	_, dup, err := r.DupIDByHash(ctx, hash)
	return dup, err
}

// ExistsProject는 해당 name이 projects에 이미 있는지 돌려준다(project dedup
// 사전 확인 — knowledge content_hash와 대칭인 자연키 확인, unique(name)).
func (r *Repo) ExistsProject(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE name = $1)`, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("exists_project: %w", err)
	}
	return exists, nil
}

// Commit은 dolt_add('-A') 후 dolt_commit('-m', msg)을 실행해 커밋 해시를 돌려준다.
// 충돌 가능성을 고려해 기본 delays(0s/1s/3s)로 Retry한다.
func (r *Repo) Commit(ctx context.Context, msg string) (string, error) {
	var hash string
	err := Retry(ctx, func() error {
		if _, err := r.pool.Exec(ctx, `SELECT dolt_add('-A')`); err != nil {
			return fmt.Errorf("dolt_add: %w", err)
		}
		var h *string // 커밋할 게 없으면 NULL일 수 있다
		if err := r.pool.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`, msg).Scan(&h); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				hash = ""
				return nil
			}
			if isNothingToCommit(err) {
				// 병렬 writer의 dolt_add('-A')가 전역 스테이징이라 이
				// 세션의 변경까지 이미 커밋한 경우(F-T50-1 실측) —
				// 데이터는 영속됐으므로 성공으로 처리한다.
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

// execSQLScript는 세미콜론 구분 SQL 스크립트를 문장별로 실행한다.
// Doltgres가 한 Exec에 여러 문장을 허용하지 않기 때문이다.
func execSQLScript(ctx context.Context, tx pgx.Tx, script string) error {
	for _, stmt := range splitStatements(script) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements는 SQL 덩어리를 문장별로 분해한다. 선행 '--' 주석 줄은
// 제거하고(Doltgres simple Exec 안전), 빈 문장은 버린다.
// flint 스키마 SQL은 문자열 리터럴에 세미콜론을 쓰지 않는다.
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

// mapInsertErr는 unique 위반(23505)을 store.ErrDuplicate으로 감싼다.
func mapInsertErr(err error) error {
	if isPgCode(err, "23505") {
		return fmt.Errorf("%w: %v", store.ErrDuplicate, err)
	}
	return err
}

// isPgCode는 err 체인 안의 PgError가 해당 SQLSTATE인지 판정한다.
func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// isNothingToCommit — dolt_commit의 "nothing to commit"(XX000).
// 스테이징이 비었다는 뜻으로, 병렬 write에서는 다른 writer가 대신
// 커밋했음을 가리킨다(F-T50-1).
func isNothingToCommit(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "XX000" &&
		strings.Contains(pgErr.Message, "nothing to commit")
}
