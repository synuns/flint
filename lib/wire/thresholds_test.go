// thresholds_test.go — shared-helper verification on the fake only
// (v1.19.6 R82: the fixtures — a fake meta map and an injected query
// failure — are fake-only; under SUPERLITE_REAL=1 the old thresholdsStore
// swapped the fake for the real store, silently discarding the fixture's
// overrides, so the test was permanently red in real mode. It now skips
// in real mode instead). An external wire_test package — after T3, when
// wire imports lib/db, internal tests cannot build due to the import cycle.
package wire_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// thresholdsFake — metaKVFake with thresholds IN-lookup routing added.
// LoadThresholds's `SELECT key, value FROM meta WHERE key IN (...)` answers
// from the meta map selecting only the thresholds keys (the rest delegates to the embedded).
type thresholdsFake struct {
	metaKVFake
	failQuery bool
}

func (f *thresholdsFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if f.failQuery {
		return nil, context.DeadlineExceeded
	}
	if strings.Contains(sql, "SELECT key, value FROM meta WHERE key IN") {
		thKeys := map[string]bool{}
		for _, k := range wire.ThresholdsKeys() {
			thKeys[k] = true
		}
		var keys []string
		for k := range f.meta {
			if thKeys[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys) // deterministic order (the thresholdsKeySQL convention)
		rows := make([][]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []any{k, f.meta[k]})
		}
		return rows, nil
	}
	return f.metaKVFake.Query(ctx, sql, args...)
}

// TestLoadThresholdsFallback — 3 contracts: (1) empty meta means every key at
// DefaultThresholds (the unset fallback — inheriting the ParseThresholds
// convention); (2) stored keys override, and a stored '0' also falls back to
// the default (treated as unset); (3) a lookup failure propagates the err —
// verbatim inheritance of the existing convention where the caller (cli doctor,
// etc.) falls back to DefaultThresholds. Fake-only (v1.19.6 R82): the meta-map
// fixtures and the injected query failure are not expressible on a real store.
func TestLoadThresholdsFallback(t *testing.T) {
	if _, real := seamRealDSN(); real {
		t.Skip("fake-only fixture: fake meta map + injected query failure (v1.19.6 R82)")
	}
	ctx := context.Background()
	def := store.DefaultThresholds()

	// 1. unset — every key at its default.
	f := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}}
	th, err := wire.LoadThresholds(ctx, f)
	if err != nil {
		t.Fatalf("빈 meta 로드 err: %v", err)
	}
	if th.StaleDays != def.StaleDays || th.StallHotH != def.StallHotH || th.DrillMaxDays != def.DrillMaxDays {
		t.Fatalf("미설정 폴백 위반: stale_days=%d stall_hot_h=%d drill_max_days=%d", th.StaleDays, th.StallHotH, th.DrillMaxDays)
	}

	// 2. override reflected + '0' falls back as unset.
	f2 := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{
		"stall_hot_h": "24",
		"stale_days":  "0",
	}}}
	th2, err := wire.LoadThresholds(ctx, f2)
	if err != nil {
		t.Fatalf("오버라이드 로드 err: %v", err)
	}
	if th2.StallHotH != 24 {
		t.Fatalf("stall_hot_h=24 미반영: %d", th2.StallHotH)
	}
	if th2.StaleDays != def.StaleDays {
		t.Fatalf("'0' 저장 키 기본값 폴백 위반: %d", th2.StaleDays)
	}

	// 3. lookup failure — err propagates (inheriting the caller-default-fallback convention).
	f3 := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}, failQuery: true}
	if _, err := wire.LoadThresholds(ctx, f3); err == nil {
		t.Fatal("조회 실패 err 미전파 — 호출부 폴백 관례 깨짐")
	}
}
