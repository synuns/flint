package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func staleRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(1), "demo-stale-topic", now.Add(-100 * 24 * time.Hour), now.Add(-100 * 24 * time.Hour), int32(0)},
	}
}

func TestListStaleDaysOverrideOverflow(t *testing.T) {
	now := time.Now()
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return staleRowsFixture(now), nil
		}
		return nil, nil
	}}}
	items, days, err := listStale(context.Background(), f, 200000, 20)
	if err != nil {
		t.Fatalf("listStale: %v", err)
	}
	if days != 200000 {
		t.Fatalf("days = %d, want 200000(오버라이드 그대로)", days)
	}
	if len(items) != 0 {
		t.Fatalf("상한 초과 오버라이드 — stale %d건, want 0(래프 시 전면 무효화)", len(items))
	}
	items, _, err = listStale(context.Background(), f, 30, 20)
	if err != nil {
		t.Fatalf("listStale(30): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("통상 오버라이드 30 — stale %d건, want 1(클램프가 원인 아님 증명)", len(items))
	}
}

func TestMaintainDaysCeilingGuard(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	t.Run("--days 상한 초과 — exit 3", func(t *testing.T) {
		out, code := runMaintainArgs(t, f, "--stale", "--days", "200000")
		if code != 3 {
			t.Fatalf("exit = %d: %s, want 3(usage계)", code, out)
		}
		if !strings.Contains(out, "ceiling") {
			t.Fatalf("출력에 상한 안내 부재: %s", out)
		}
	})
	t.Run("--days 상한 경계값 — 통과", func(t *testing.T) {
		_, code := runMaintainArgs(t, f, "--stale", "--days", "106751")
		if code == 3 || code == 1 {
			t.Fatalf("exit = %d, want 0(상한 그대로는 허용)", code)
		}
	})
	t.Run("--expire-pending 상한 초과 — exit 1", func(t *testing.T) {
		out, code := runMaintainArgs(t, f, "--expire-pending", "200000", "--commit")
		if code != 1 {
			t.Fatalf("exit = %d: %s, want 1", code, out)
		}
		if !strings.Contains(out, "ceiling") {
			t.Fatalf("출력에 상한 안내 부재: %s", out)
		}
	})
	t.Run("--prune-invalidated 상한 초과 — exit 1", func(t *testing.T) {
		out, code := runMaintainArgs(t, f, "--prune-invalidated", "200000", "--commit")
		if code != 1 {
			t.Fatalf("exit = %d: %s, want 1", code, out)
		}
		if !strings.Contains(out, "ceiling") {
			t.Fatalf("출력에 상한 안내 부재: %s", out)
		}
	})
}

func TestMeteringFreshnessCorruptGateKey(t *testing.T) {
	ctx := context.Background()
	th := store.DefaultThresholds()
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "last_threshold_snapshot_at") {
			return [][]any{
				{"last_threshold_snapshot_at", "claim-1a2b3c"},
				{"last_rollup_at", time.Now().UTC().Format(time.RFC3339)},
			}, nil
		}
		return meteringFreshnessFn(nil, nil)(sql, args)
	}
	c := checkMeteringFreshnessTh(ctx, &fakeStore{queryFn: fn}, th)
	if c.Status != "warn" {
		t.Fatalf("status=%q detail=%q, want warn", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "last_threshold_snapshot_at (corrupt)") {
		t.Fatalf("detail %q — (corrupt) 표기 미포함", c.Detail)
	}
	if strings.Contains(c.Detail, "last_threshold_snapshot_at (never)") {
		t.Fatalf("detail %q — corrupt가 (never)로 둔갑", c.Detail)
	}
}

func TestMeteringFreshnessWarnHDetailClamped(t *testing.T) {
	ctx := context.Background()
	th := store.DefaultThresholds()
	th.MeteringFreshnessWarnH = 3000000
	c := checkMeteringFreshnessTh(ctx,
		&fakeStore{queryFn: meteringFreshnessFn(nil, nil)}, th)
	if c.Status != "ok" {
		t.Fatalf("status=%q detail=%q, want ok", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "87600h") {
		t.Fatalf("detail %q — 클램프 후 87600h 미표기", c.Detail)
	}
	if strings.Contains(c.Detail, "3000000") {
		t.Fatalf("detail %q — 클램프 전 원값 잔존", c.Detail)
	}
}

func TestSessionLabelMaxAgeOverflow(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"events_retention_days", "200000"}}, nil
		}
		return nil, nil
	}}
	got := sessionLabelMaxAge(context.Background(), f)
	if got <= 0 {
		t.Fatalf("sessionLabelMaxAge(200000) = %v, want 양수(클램프 — 랩 시 전 라벨 즉시 만료)", got)
	}
}
