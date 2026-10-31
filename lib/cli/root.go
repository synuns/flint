// Package cli — flint 커맨드 진입점. os.Args 수동 파싱(프롬프트·TUI 금지),
// 출력은 한 줄 JSON 또는 고정 포맷, exit code 0/1/2/3 계약을 지킨다.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/db"
	"flint/lib/store"
)

// defaultDSN — FLINT_DSN 미지정 시 기본 접속 문자열(ADR-0010: 포트 5434).
const defaultDSN = "postgresql://flint:flint-dev@localhost:5434/flint"

// Store — 커맨드가 필요한 저장소 연산. cli_test는 fake으로 주입한다.
type Store interface {
	Migrate(ctx context.Context) error
	InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error)
	ExistsHash(ctx context.Context, hash string) (bool, error)
	ExistsProject(ctx context.Context, name string) (bool, error)
	Commit(ctx context.Context, msg string) (string, error)
	Ping(ctx context.Context) error
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()
}

// newStore — seam. 기본 실구현, 테스트는 fake으로 교체(SUPERLITE_REAL=1은 실구현 위임).
var newStore = func(ctx context.Context, dsn string) (Store, error) {
	return newRealStore(ctx, dsn)
}

// dsnFromEnv — DSN 해결 진입점. T74부터 ResolveDSN 위임:
// FLINT_DSN > config.json(LoadConfigDSN) > 기본값.
func dsnFromEnv() string {
	return ResolveDSN()
}

// Run은 args로 커맨드를 실행해 stdout에 기록하고 exit code를 반환한다.
func Run(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, usageLine())
		return 1
	}
	cmd, rest := args[0], args[1:]
	// help도 store-free fast path(코디네이터 룰링): registry 핸들러가
	// 아니라 접속 전에 직접 출력한다. 인자(`help foo`)는 무시(R1).
	if cmd == "help" {
		return runHelp(stdout)
	}
	run, ok := Lookup(cmd)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"unknown command: `+cmd+`"}`)
		return 1
	}
	ctx := context.Background()
	// version·setup·teardown·mcp는 store-free — DB 미기동에도 즉시 응답
	// (§13, T33 룰링 · T40 확장: DB 다운이 setup의 주 시나리오 · T57: mcp는
	// 도구 호출이 cli.Run으로 각자 store를 연결한다). nudge는 v0.13.2 N5부터
	// store 경유 — nudgeProfile가 exposure_profile을 조회해 minimal 계약을
	// 지킨다(구형 store-free 시 minimal 자동 종결 불가).
	if cmd == "version" || cmd == "setup" || cmd == "teardown" || cmd == "mcp" {
		return run(ctx, nil, rest, stdin, stdout)
	}
	st, err := newStore(ctx, dsnFromEnv())
	if err != nil {
		// compact는 훅 진입 커맨드 — DB 부재도 조용히 exit 0(§3 비간섭).
		// st nil로 실행하면 runCompact가 무동작 0을 반환한다.
		if cmd == "compact" {
			return run(ctx, nil, rest, stdin, stdout)
		}
		// nudge도 훅 진입 커맨드(N5) — 에러 JSON을 stdout에 흘리지 않고
		// 조용히 MapExit 코드로만 종료한다(서버 다운=2, 훅 quiet 계약).
		if cmd == "nudge" {
			return store.MapExit(err)
		}
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	return run(ctx, st, rest, stdin, stdout)
}

// runHelp — `flint help` 출력. usage JSON 1줄 + 명령 요약 1줄, exit 0.
// help는 인자 없음 — 남는 인자는 무시한다(R1).
func runHelp(w io.Writer) int {
	fmt.Fprintln(w, usageLine())
	fmt.Fprintln(w, helpSummary())
	return 0
}

// helpSummary — 명령별 1줄 요약(v0.10.2 R1).
func helpSummary() string {
	return "write 기록 · read 검색 · hot 주입 · status 상태 · doctor 진단 · report 관측 · backup/restore 백업 · maintain/supersede · approve 승격 · done/compact/nudge 훅 · setup/teardown 설치 · audit/diff 시간여행 · mcp 서버 · version"
}

// errLine — 에러를 한 줄 JSON으로. 서버 다운은 조용히(server_down) 한 줄만.
func errLine(err error) string {
	m := map[string]string{"error": "internal", "message": err.Error()}
	var se *store.SchemaError
	switch {
	case errors.As(err, &se):
		m = map[string]string{"error": "schema", "field": se.Field, "expect": se.Expect}
	case errors.Is(err, store.ErrServerDown):
		m = map[string]string{"error": "server_down"}
	case errors.Is(err, store.ErrRetriesExhausted):
		m = map[string]string{"error": "retries_exhausted"}
	case errors.Is(err, store.ErrDuplicate):
		m = map[string]string{"error": "duplicate"}
	}
	b, _ := json.Marshal(m)
	return string(b)
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
	if err := ensureSchemas(ctx, repo); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// 2차 게이트 — 신규 DB는 Migrate가 schema_version=1로 테이블을 만들므로
	// 여기서 v2→v4로 자연 승격된다(기존 배포 경로 동일). 승격된 DB는 수치
	// 비교 단락 no-op라 매 접속 부담 없다.
	if err := ensureSchemas(ctx, repo); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// ensureSchemas — 버전 게이트 v2→v3→v4→v5 순차 승격(newRealStore 전용).
// 순서 불변: v1 DB가 v5로 건너뛰며 v2 컬럼·v3 인덱스·v4 컬럼을 놓치지 않게 한다.
func ensureSchemas(ctx context.Context, repo *db.Repo) error {
	// v2 승격(코디네이터 룰링): sessions.tasks·projects.note 컬럼.
	if err := repo.EnsureSchemaV2(ctx); err != nil {
		return err
	}
	// v3 승격(T51/T57 배선): 필터 경로 인덱스 3종(ADR-0008).
	if err := repo.EnsureSchemaV3(ctx); err != nil {
		return err
	}
	// v4 승격(T62 배선): 참조 계측 컬럼(read_count·last_read_at).
	if err := repo.EnsureSchemaV4(ctx); err != nil {
		return err
	}
	// v5 승격(T82 배선): 계보 컬럼(superseded_by) — 추가만, 기존 게이트 불변.
	return repo.EnsureSchemaV5(ctx)
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

// queryOnce — reader 계열 재시도 1회 후 실패 전파(spec §7 읽기).
func queryOnce(ctx context.Context, st Store, sql string, args []any) ([][]any, error) {
	rows, err := st.Query(ctx, sql, args...)
	if err != nil {
		rows, err = st.Query(ctx, sql, args...)
	}
	return rows, err
}
