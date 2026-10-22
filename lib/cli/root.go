// Package cli — flint 커맨드 진입점. os.Args 수동 파싱(프롬프트·TUI 금지),
// 출력은 한 줄 JSON 또는 고정 포맷, exit code 0/1/2/3 계약을 지킨다.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"flint/lib/db"
	"flint/lib/store"
)

// defaultDSN — FLINT_DSN 미지정 시 기본 접속 문자열(ADR-0001: 포트 5433).
const defaultDSN = "postgresql://flint:flint-dev@localhost:5433/flint"

// Store — 커맨드가 필요한 저장소 연산. cli_test는 fake으로 주입한다.
type Store interface {
	Migrate(ctx context.Context) error
	InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error)
	ExistsHash(ctx context.Context, hash string) (bool, error)
	Commit(ctx context.Context, msg string) (string, error)
	Ping(ctx context.Context) error
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close()
}

// newStore — seam. 기본 실구현, 테스트는 fake으로 교체(SUPERLITE_REAL=1은 실구현 위임).
var newStore = func(ctx context.Context, dsn string) (Store, error) {
	return newRealStore(ctx, dsn)
}

// dsnFromEnv — FLINT_DSN 우선, 없으면 기본값.
func dsnFromEnv() string {
	if d := os.Getenv("FLINT_DSN"); d != "" {
		return d
	}
	return defaultDSN
}

// Run은 args로 커맨드를 실행해 stdout에 기록하고 exit code를 반환한다.
func Run(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, usageLine())
		return 1
	}
	cmd, rest := args[0], args[1:]
	run, ok := Lookup(cmd)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"unknown command: `+cmd+`"}`)
		return 1
	}
	ctx := context.Background()
	// version·setup·teardown·mcp는 store-free — DB 미기동에도 즉시 응답
	// (§13, T33 룰링 · T40 확장: DB 다운이 setup의 주 시나리오 · T57: mcp는
	// 도구 호출이 cli.Run으로 각자 store를 연결한다).
	if cmd == "version" || cmd == "setup" || cmd == "teardown" || cmd == "mcp" {
		return run(ctx, nil, rest, stdin, stdout)
	}
	st, err := newStore(ctx, dsnFromEnv())
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	return run(ctx, st, rest, stdin, stdout)
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
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// v2 승격(코디네이터 룰링): Migrate(v1 소유) 뒤 버전 게이트로 승격 —
	// 모든 실DB 커맨드 경로에서 자연 승격(하루 첫 세션 트리거 정신과 정합).
	if err := repo.EnsureSchemaV2(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// v3 승격(T51/T57 배선): 필터 경로 인덱스 3종(ADR-0008). v2와 같은
	// 자연 승격 경로 — 이미 v3면 no-op라 매 접속 부담 없다.
	if err := repo.EnsureSchemaV3(ctx); err != nil {
		s.Close()
		return nil, err
	}
	// v4 승격(T62 배선): 참조 계측 컬럼(read_count·last_read_at). 동일 경로.
	if err := repo.EnsureSchemaV4(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
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
