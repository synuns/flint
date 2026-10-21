// repo.go — flint 저장소 접근 계층(T7). 스키마 마이그레이션·삽입·dolt 커밋.
package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// kind → 대상 테이블. write는 INSERT 전용이라 각 kind마다 단일 테이블에만 쓴다.
var kindTables = map[string]string{
	"knowledge": "knowledge",
	"session":   "sessions",
	"project":   "projects",
}

// Repo는 Doltgres 풀을 감싼 저장소다.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo는 접속 후 Repo를 돌려준다. 실패는 ErrServerDown으로 전파된다.
func NewRepo(ctx context.Context, dsn string) (*Repo, error) {
	p, err := Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Repo{pool: p}, nil
}

// Close는 풀을 정리한다.
func (r *Repo) Close() { r.pool.Close() }

// Migrate는 스키마가 없을 때 SchemaSQL+ViewsSQL을 실행하고
// meta.schema_version=1을 기록한다. 이미 1이면 아무것도 하지 않는다(멱등).
func (r *Repo) Migrate(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	switch {
	case err == nil && version == "1":
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, "42P01"): // meta 테이블 없음 = 최초
		// 스키마 생성 경로로 진행
	default:
		return fmt.Errorf("migrate: check schema_version: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schemaSQLFn()); err != nil {
		return fmt.Errorf("migrate: schema: %w", err)
	}
	if _, err := tx.Exec(ctx, viewsSQLFn()); err != nil {
		return fmt.Errorf("migrate: views: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '1')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("migrate: meta: %w", err)
	}
	return tx.Commit(ctx)
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

// ExistsHash는 해당 content_hash가 knowledge에 이미 있는지 돌려준다(dedup 사전 확인).
func (r *Repo) ExistsHash(ctx context.Context, hash string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM knowledge WHERE content_hash = $1)`, hash).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("exists_hash: %w", err)
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
