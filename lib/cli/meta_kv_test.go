// meta_kv_test.go — (spec §1.2 / R12): the meta KV consumer side (increment
// delegation and single-source guards). Default metaKVFake; with SUPERLITE_REAL=1,
// a delegatingFake goes through the real implementation (unstubbed operations delegate to the real DB).
// Direct Store round trips (TestMetaKVStoreRoundtrip) moved to the lib/wire
// package tests — this file covers only the cli consumer handler paths.
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

// metaKVFake — a Store fake simulating the meta table with a map.
// It applies the SQL semantics of meta_kv.go as-is (single-key SELECT, $1/$2 upsert, $1/'1'
// atomic increment).
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
		// atomic increment upsert — if the existing value is a positive-integer notation, +1; otherwise reset to 1.
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

// metaKVStore — the default metaKVFake; with FLINT_TEST_DSN set + SUPERLITE_REAL=1 it returns the
// real implementation wrapped in a delegatingFake (the real-code path seam for the producer Store).
// Without FLINT_TEST_DSN, no real-implementation fallback (seamRealDSN N1).
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

// metaKVCleanup — restores the test keys when the real-DB seam runs (unnecessary for the local map).
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

// TestBumpFallbackAtomic — bumpReadFallbackToMeta increments via a single
// atomic upsert (removing the preceding SELECT->INSERT round trip). 2 fallbacks from 0 -> "2".
func TestBumpFallbackAtomic(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	metaKVCleanup(t, st, query.ReadFallbackCountKey)
	// the increment expectation is pinned — even if a leftover counter exists on the real DB, restart from 0.
	if err := setMetaKV(ctx, st, query.ReadFallbackCountKey, "0"); err != nil {
		t.Fatalf("초기화 err: %v", err)
	}
	n0 := len(f.fakeStore.queries)
	bumpReadFallbackToMeta(ctx, st)
	bumpReadFallbackToMeta(ctx, st)
	// each increment is 1 statement — only INSERT ... ON CONFLICT is issued, no SELECT round trip.
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

// TestMetaKVSingleSource — zero meta SQL literal residue in the owning files (metrics, tuning, setup,
// hot, read, done, backup) — all reads and increments delegate to the
// single meta_kv.go entry point.
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

// TestBumpMetaCounterDelegates — the done recall-counter increment uses the
// single bumpMetaKV entry (inheriting the corrupt-value reset-to-1 guard — preventing regression of
// the old implementation where an ungated cast error froze the counter).
func TestBumpMetaCounterDelegates(t *testing.T) {
	ctx := context.Background()
	f := newMetaKVFake()
	st := metaKVStore(t, f)
	metaKVCleanup(t, st, "recall_starved_count")
	// seed a corrupt value — without the reset guard, a cast error freezes the increment.
	if err := setMetaKV(ctx, st, "recall_starved_count", "corrupt"); err != nil {
		t.Fatalf("시드 err: %v", err)
	}
	bumpMetaCounter(ctx, st, "recall_starved_count")
	bumpMetaCounter(ctx, st, "recall_starved_count")
	if v, ok := getMetaKV(ctx, st, "recall_starved_count"); !ok || v != "2" {
		t.Fatalf("recall_starved_count = (%q,%v), want (\"2\",true) — 파손값 1 리셋 후 증분", v, ok)
	}
}
