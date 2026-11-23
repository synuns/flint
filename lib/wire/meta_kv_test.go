package wire_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

type metaKVFake struct {
	meta    map[string]string
	queries []string
	wire.HistoryStubs
}

func (f *metaKVFake) Migrate(ctx context.Context) error { return nil }
func (f *metaKVFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *metaKVFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *metaKVFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *metaKVFake) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *metaKVFake) Ping(ctx context.Context) error { return nil }
func (f *metaKVFake) Close()                         {}

func (f *metaKVFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	switch {
	case strings.Contains(sql, "SELECT value FROM meta"):
		k := fmt.Sprint(args[0])
		if v, ok := f.meta[k]; ok {
			return [][]any{{v}}, nil
		}
		return nil, nil
	case strings.Contains(sql, "INSERT INTO meta") && strings.Contains(sql, "'1')"):

		k := fmt.Sprint(args[0])
		if n, err := strconv.Atoi(f.meta[k]); err == nil && n >= 0 {
			f.meta[k] = strconv.Itoa(n + 1)
		} else {
			f.meta[k] = "1"
		}
		return nil, nil
	case strings.Contains(sql, "INSERT INTO meta"):
		f.meta[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
		return nil, nil
	}
	return nil, nil
}

type poolStore struct {
	pool *pgxpool.Pool
	wire.HistoryStubs
}

func (p *poolStore) Migrate(ctx context.Context) error { return nil }
func (p *poolStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (p *poolStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (p *poolStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (p *poolStore) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (p *poolStore) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *poolStore) Close()                         { p.pool.Close() }
func (p *poolStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
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

func seamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

func metaKVStore(t *testing.T, f *metaKVFake) wire.Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		pool, err := db.Connect(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		s := &poolStore{pool: pool}
		t.Cleanup(s.Close)
		return s
	}
	return f
}

func metaKVCleanup(t *testing.T, st wire.Store, keys ...string) {
	t.Helper()
	if _, real := seamRealDSN(); !real {
		return
	}
	ctx := context.Background()
	for _, k := range keys {
		key := k
		t.Cleanup(func() {
			_, _ = wire.QueryOnce(ctx, st, `DELETE FROM meta WHERE key = $1`, []any{key})
		})
	}
}

func TestMetaKVStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	f := &metaKVFake{meta: map[string]string{}}
	st := metaKVStore(t, f)
	key := "meta_kv_roundtrip_test"
	metaKVCleanup(t, st, key)
	if err := wire.SetMetaKV(ctx, st, key, "v"); err != nil {
		t.Fatalf("SetMetaKV err: %v", err)
	}
	if v, ok := wire.GetMetaKV(ctx, st, key); v != "v" || !ok {
		t.Fatalf("GetMetaKV(ctx,st,\"k\") = (%q,%v), want (\"v\",true)", v, ok)
	}
	if err := wire.SetMetaKV(ctx, st, key, "v2"); err != nil {
		t.Fatalf("SetMetaKV 갱신 err: %v", err)
	}
	if v, ok := wire.GetMetaKV(ctx, st, key); v != "v2" || !ok {
		t.Fatalf("upsert 덮어쓰기 실패: GetMetaKV = (%q,%v), want (\"v2\",true)", v, ok)
	}
	if v, ok := wire.GetMetaKV(ctx, st, "meta_kv_absent_test"); v != "" || ok {
		t.Fatalf("미존재 키: GetMetaKV = (%q,%v), want (\"\",false)", v, ok)
	}
}

type bumpFailFake struct {
	metaKVFake
	calls int
}

func (f *bumpFailFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.calls++
	if strings.Contains(sql, "INSERT INTO meta") && strings.Contains(sql, "'1')") {
		return nil, errors.New("ambiguous write failure")
	}
	return f.metaKVFake.Query(ctx, sql, args...)
}

func TestBumpMetaKVRunsSingleAttempt(t *testing.T) {
	ctx := context.Background()
	f := &bumpFailFake{metaKVFake: metaKVFake{meta: map[string]string{}}}
	if err := wire.BumpMetaKV(ctx, f, "bump_once_probe"); err == nil {
		t.Fatalf("BumpMetaKV 오류 = nil, want 실패 전파(호출부 계측 원칙의 전제)")
	}
	if f.calls != 1 {
		t.Fatalf("Query 실행 %d회, want 1 — 비멱등 증분의 재시도는 이중 계상", f.calls)
	}
}
