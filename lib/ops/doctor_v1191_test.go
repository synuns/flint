package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func TestMeteringFreshnessFutureStampStale(t *testing.T) {
	ctx := context.Background()
	th := store.DefaultThresholds()
	future := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, src := range []string{"last_rollup_at", "last_threshold_snapshot_at", "read_events"} {
		t.Run(src, func(t *testing.T) {
			c := checkMeteringFreshnessTh(ctx,
				&fakeStore{queryFn: meteringFreshnessFn(map[string]time.Time{src: future}, nil)}, th)
			if c.Status != "warn" {
				t.Fatalf("status=%q detail=%q, want warn(미래 스탬프 = stale)", c.Status, c.Detail)
			}
			if !strings.Contains(c.Detail, src) {
				t.Fatalf("detail %q — 미래 스탬프 소스명 미포함", c.Detail)
			}
			if !strings.Contains(c.Detail, "future") {
				t.Fatalf("detail %q — future 표기 미포함", c.Detail)
			}
		})
	}
}

func TestMeteringFreshnessWarnHClamp(t *testing.T) {
	ctx := context.Background()
	run := func(th store.Thresholds) DoctorCheck {
		return checkMeteringFreshnessTh(ctx,
			&fakeStore{queryFn: meteringFreshnessFn(nil, nil)}, th)
	}
	t.Run("3000000h 오버라이드 — 전 소스 1h 신선 → ok", func(t *testing.T) {
		th := store.DefaultThresholds()
		th.MeteringFreshnessWarnH = 3000000
		if c := run(th); c.Status != "ok" {
			t.Fatalf("status=%q detail=%q, want ok(클램프 미적용 — 랩 결함)", c.Status, c.Detail)
		}
	})
	t.Run("정상 임계 48h — 100h 경과 소스 → warn", func(t *testing.T) {
		th := store.DefaultThresholds()
		th.MeteringFreshnessWarnH = 48
		c := checkMeteringFreshnessTh(ctx,
			&fakeStore{queryFn: meteringFreshnessFn(map[string]time.Time{
				"hot_events": time.Now().Add(-100 * time.Hour),
			}, nil)}, th)
		if c.Status != "warn" || !strings.Contains(c.Detail, "hot_events") {
			t.Fatalf("status=%q detail=%q, want warn+hot_events(정상 임계 동작)", c.Status, c.Detail)
		}
	})
}
