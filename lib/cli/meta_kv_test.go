package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
)

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

func TestBumpFallbackAtomic(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	metaKVCleanup(t, st, query.ReadFallbackCountKey)

	if err := setMetaKV(ctx, st, query.ReadFallbackCountKey, "0"); err != nil {
		t.Fatalf("초기화 err: %v", err)
	}
	n0 := len(f.fakeStore.queries)
	bumpReadFallbackToMeta(ctx, st)
	bumpReadFallbackToMeta(ctx, st)

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

func TestMetaKVSingleSource(t *testing.T) {
	for _, f := range []string{"../ops/metrics.go", "../ops/tuning.go", "setup.go", "../knowledge/hot.go", "../knowledge/read.go", "../knowledge/done.go", "../ops/backup.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, lit := range []string{"INSERT INTO meta", "SELECT value FROM meta"} {
			if strings.Contains(src, lit) {
				t.Errorf("%s: meta SQL 리터럴 잔존(%q) — meta_kv.go 단일 진입 위임 필요", f, lit)
			}
		}
	}
}

func TestBumpMetaCounterDelegates(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	metaKVCleanup(t, st, "recall_starved_count")

	if err := setMetaKV(ctx, st, "recall_starved_count", "corrupt"); err != nil {
		t.Fatalf("시드 err: %v", err)
	}
	bumpMetaCounter(ctx, st, "recall_starved_count")
	bumpMetaCounter(ctx, st, "recall_starved_count")
	if v, ok := getMetaKV(ctx, st, "recall_starved_count"); !ok || v != "2" {
		t.Fatalf("recall_starved_count = (%q,%v), want (\"2\",true) — 파손값 1 리셋 후 증분", v, ok)
	}
}
