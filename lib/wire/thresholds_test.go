package wire_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

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
		sort.Strings(keys)
		rows := make([][]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []any{k, f.meta[k]})
		}
		return rows, nil
	}
	return f.metaKVFake.Query(ctx, sql, args...)
}

func TestLoadThresholdsFallback(t *testing.T) {
	if _, real := seamRealDSN(); real {
		t.Skip("fake-only fixture: fake meta map + injected query failure (v1.19.6 R82)")
	}
	ctx := context.Background()
	def := store.DefaultThresholds()

	f := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}}
	th, err := wire.LoadThresholds(ctx, f)
	if err != nil {
		t.Fatalf("빈 meta 로드 err: %v", err)
	}
	if th.StaleDays != def.StaleDays || th.StallHotH != def.StallHotH || th.DrillMaxDays != def.DrillMaxDays {
		t.Fatalf("미설정 폴백 위반: stale_days=%d stall_hot_h=%d drill_max_days=%d", th.StaleDays, th.StallHotH, th.DrillMaxDays)
	}

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

	f3 := &thresholdsFake{metaKVFake: metaKVFake{meta: map[string]string{}}, failQuery: true}
	if _, err := wire.LoadThresholds(ctx, f3); err == nil {
		t.Fatal("조회 실패 err 미전파 — 호출부 폴백 관례 깨짐")
	}
}
