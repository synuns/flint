// meta_kv_test.go — v0.34.3 T2 meta KV 라운드트립(v0.38.0 §1: lib/cli에서
// 계약층으로 이동·패키지명 갱신). 기본 metaKVFake, SUPERLITE_REAL=1 +
// FLINT_TEST_DSN 설정 시 poolStore로 실DB 경유(seam 검증).
package wire

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
)

// metaKVFake — meta 테이블을 map으로 시뮬레이션하는 Store fake.
// meta.go의 SQL 의미(단일 키 SELECT·$1/$2 upsert·$1/'1' 원자 증분)를
// 그대로 적용한다.
type metaKVFake struct {
	meta    map[string]string
	queries []string
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
		// 원자 증분 upsert — 기존값이 양의 정수 표기면 +1, 아니면 1 리셋.
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

// poolStore — 실DB seam용 최소 Store(읽기·쓰기는 풀 Query 위임).
type poolStore struct {
	pool *pgxpool.Pool
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

// seamRealDSN — SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만 실DB 접속
// 허용(운영 폴백 차단 — cli seamRealDSN N1 계약 승계).
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

// metaKVStore — 기본 metaKVFake, SUPERLITE_REAL=1이면 poolStore로 실구현
// 경로 반환(생산자 Store의 실코드 seam). 실DB 미기동 시 스킵.
func metaKVStore(t *testing.T, f *metaKVFake) Store {
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

// metaKVCleanup — 실DB seam 실행 시 테스트 키 원복(로컬 map은 불필요).
func metaKVCleanup(t *testing.T, st Store, keys ...string) {
	t.Helper()
	if _, real := seamRealDSN(); !real {
		return
	}
	ctx := context.Background()
	for _, k := range keys {
		key := k
		t.Cleanup(func() {
			_, _ = QueryOnce(ctx, st, `DELETE FROM meta WHERE key = $1`, []any{key})
		})
	}
}

// TestMetaKVStoreRoundtrip — Review Focus: SetMetaKV 후 GetMetaKV 왕복.
// 신규 키 set→get·갱신 upsert 덮어쓰기·미존재 키 ("", false).
// 이름 참고: TestMetaKVRoundtrip은 v0140_fix_test.go(cli)가 클로저
// 왕복으로 이미 사용 — 스펙 지정명과 충돌해 Store 직접 왕복임을 드러낸다.
func TestMetaKVStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	f := &metaKVFake{meta: map[string]string{}}
	st := metaKVStore(t, f)
	key := "meta_kv_roundtrip_test"
	metaKVCleanup(t, st, key)
	if err := SetMetaKV(ctx, st, key, "v"); err != nil {
		t.Fatalf("SetMetaKV err: %v", err)
	}
	if v, ok := GetMetaKV(ctx, st, key); v != "v" || !ok {
		t.Fatalf("GetMetaKV(ctx,st,\"k\") = (%q,%v), want (\"v\",true)", v, ok)
	}
	if err := SetMetaKV(ctx, st, key, "v2"); err != nil {
		t.Fatalf("SetMetaKV 갱신 err: %v", err)
	}
	if v, ok := GetMetaKV(ctx, st, key); v != "v2" || !ok {
		t.Fatalf("upsert 덮어쓰기 실패: GetMetaKV = (%q,%v), want (\"v2\",true)", v, ok)
	}
	if v, ok := GetMetaKV(ctx, st, "meta_kv_absent_test"); v != "" || ok {
		t.Fatalf("미존재 키: GetMetaKV = (%q,%v), want (\"\",false)", v, ok)
	}
}
