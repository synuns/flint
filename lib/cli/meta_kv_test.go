// meta_kv_test.go — v0.34.3 T2(스펙 §1.2 / R12): meta KV 단일 진입점
// roundtrip·폴백 증분 원자화. 기본 metaKVFake, SUPERLITE_REAL=1이면
// delegatingFake로 실구현 경유(스텑 미지정 연산은 실DB 위임).
package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
)

// metaKVFake — meta 테이블을 map으로 시뮬레이션하는 Store fake.
// meta_kv.go의 SQL 의미(단일 키 SELECT·$1/$2 upsert·$1/'1' 원자 증분)를
// 그대로 적용한다.
type metaKVFake struct {
	*fakeStore
	meta map[string]string
}

func newMetaKVFake() *metaKVFake {
	return &metaKVFake{fakeStore: &fakeStore{}, meta: map[string]string{}}
}

func (f *metaKVFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.fakeStore.queries = append(f.fakeStore.queries, sql)
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

// metaKVStore — 기본 metaKVFake, FLINT_TEST_DSN 설정 + SUPERLITE_REAL=1이면
// delegatingFake로 실구현을 랩해 반환(생산자 Store의 실코드 경로 seam).
// FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(seamRealDSN N1).
func metaKVStore(t *testing.T, f *metaKVFake) Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		s, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(s.Close)
		return &delegatingFake{fake: f.fakeStore, real: s}
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
			_, _ = queryOnce(ctx, st, `DELETE FROM meta WHERE key = $1`, []any{key})
		})
	}
}

// TestMetaKVStoreRoundtrip — Review Focus: setMetaKV 후 getMetaKV 왕복.
// 신규 키 set→get·갱신 upsert 덮어쓰기·미존재 키 ("", false).
// 이름 참고: TestMetaKVRoundtrip은 v0140_fix_test.go가 클로저(metaKV)
// 왕복으로 이미 사용 — 스펙 지정명과 충돌해 Store 직접 왕복임을 드러낸다.
func TestMetaKVStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	key := "meta_kv_roundtrip_test"
	metaKVCleanup(t, st, key)
	if err := setMetaKV(ctx, st, key, "v"); err != nil {
		t.Fatalf("setMetaKV err: %v", err)
	}
	if v, ok := getMetaKV(ctx, st, key); v != "v" || !ok {
		t.Fatalf("getMetaKV(ctx,st,\"k\") = (%q,%v), want (\"v\",true)", v, ok)
	}
	if err := setMetaKV(ctx, st, key, "v2"); err != nil {
		t.Fatalf("setMetaKV 갱신 err: %v", err)
	}
	if v, ok := getMetaKV(ctx, st, key); v != "v2" || !ok {
		t.Fatalf("upsert 덮어쓰기 실패: getMetaKV = (%q,%v), want (\"v2\",true)", v, ok)
	}
	if v, ok := getMetaKV(ctx, st, "meta_kv_absent_test"); v != "" || ok {
		t.Fatalf("미존재 키: getMetaKV = (%q,%v), want (\"\",false)", v, ok)
	}
}

// TestBumpFallbackAtomic — Review Focus: bumpReadFallbackToMeta가 단일
// 원자 upsert로 증분한다(선행 SELECT→INSERT 왕복 제거). 0에서 2회 폴백 → "2".
func TestBumpFallbackAtomic(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	metaKVCleanup(t, st, query.ReadFallbackCountKey)
	// 증분 기대값 고정 — 실DB에 잔존 카운터가 있어도 0에서 재시작.
	if err := setMetaKV(ctx, st, query.ReadFallbackCountKey, "0"); err != nil {
		t.Fatalf("초기화 err: %v", err)
	}
	n0 := len(f.fakeStore.queries)
	bumpReadFallbackToMeta(ctx, st)
	bumpReadFallbackToMeta(ctx, st)
	// 증분은 각 1문장 — SELECT 왕복 없이 INSERT…ON CONFLICT만 발행.
	bumps := f.fakeStore.queries[n0:]
	if len(bumps) != 2 {
		t.Fatalf("증분 발행 질의 %d건, want 2: %v", len(bumps), bumps)
	}
	for _, q := range bumps {
		if !strings.Contains(q, "INSERT INTO meta") || !strings.Contains(q, "ON CONFLICT") {
			t.Fatalf("원자 upsert 아님: %q", q)
		}
		if strings.Contains(q, "SELECT") {
			t.Fatalf("증분 경로에 SELECT 왕복 잔존: %q", q)
		}
	}
	if v, ok := getMetaKV(ctx, st, query.ReadFallbackCountKey); !ok || v != "2" {
		t.Fatalf("read_fallback_calls = (%q,%v), want (\"2\",true)", v, ok)
	}
}
