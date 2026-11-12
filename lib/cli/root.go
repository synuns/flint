// Package cli — flint 커맨드 진입점. os.Args 수동 파싱(프롬프트·TUI 금지),
// 출력은 한 줄 JSON 또는 고정 포맷, exit code 0/1/2/3 계약을 지킨다.
// v0.38.0 §1: 디스패치·계약은 lib/wire로 추출 — 이 파일은 realStore
// 어댑터와 cli.Run 위임 래퍼만 소유한다.
package cli

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
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

// ensureSchemas — 버전 게이트 v2→v3→v4→v5→v6→v7 순차 승격(newRealStore 전용).
// 순서 불변: v1 DB가 v7로 건너뛰며 중간 컬럼·인덱스·테이블을 놓치지 않게 한다.
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
	if err := repo.EnsureSchemaV5(ctx); err != nil {
		return err
	}
	// v6 승격(v0.19.0 배선): 효과 계측 테이블(read_events) — 추가만.
	if err := repo.EnsureSchemaV6(ctx); err != nil {
		return err
	}
	// v7 승격(v0.20.0 배선): 계측 정밀화(ids·session_starts·hot_events).
	if err := repo.EnsureSchemaV7(ctx); err != nil {
		return err
	}
	// v8 승격(v0.30.0 배선): hit 품질 근사(first_rank) — 추가만.
	return repo.EnsureSchemaV8(ctx)
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
