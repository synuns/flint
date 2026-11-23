package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func seedSnapshotPerf(t *testing.T, ctx context.Context, r *Repo, keys, perKey int) map[string]string {
	t.Helper()
	if _, err := r.pool.Exec(ctx, `DELETE FROM threshold_snapshots`); err != nil {
		t.Fatalf("시드 초기화: %v", err)
	}

	if _, err := r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO threshold_snapshots (ts, key, old_value, new_value)
		SELECT now() - (((%d - g)::text || ' minutes')::interval),
		       'perf-k' || k, 'x', 'v' || k || '-' || g
		FROM generate_series(0, %d) k, generate_series(0, %d) g`,
		perKey, keys-1, perKey-1)); err != nil {
		t.Fatalf("시드(%d키×%d행): %v", keys, perKey, err)
	}
	want := map[string]string{}
	for k := 0; k < keys; k++ {
		want[fmt.Sprintf("perf-k%d", k)] = fmt.Sprintf("v%d-%d", k, perKey-1)
	}
	return want
}

func TestLastThresholdSnapshotsJoinSlow(t *testing.T) {
	ctx := context.Background()
	r := newScratchTestRepo(t, ctx)
	if r == nil {
		t.Skip("scratch DB unavailable — the perf shape needs the real engine")
	}

	t.Run("52키×96행(~5,000행) — 값 정합+소요 상한", func(t *testing.T) {
		want := seedSnapshotPerf(t, ctx, r, 52, 96)
		start := time.Now()
		got, err := r.lastThresholdSnapshots(ctx)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("lastThresholdSnapshots: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("키 수 = %d, want %d", len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("key %q = %q, want 최신값 %q", k, got[k], v)
			}
		}
		if elapsed > 2*time.Second {
			t.Fatalf("소요 %v — 상한 2s 초과(상관 서브쿼리 퇴행 재현 의심)", elapsed)
		}
		t.Logf("52키×96행 조회 소요 %v", elapsed)
	})

	t.Run("52키×6행(303행 형태) — 값 정합", func(t *testing.T) {
		want := seedSnapshotPerf(t, ctx, r, 52, 6)
		got, err := r.lastThresholdSnapshots(ctx)
		if err != nil {
			t.Fatalf("lastThresholdSnapshots: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("키 수 = %d, want %d", len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("key %q = %q, want 최신값 %q", k, got[k], v)
			}
		}
	})

	t.Run("빈 테이블 — 빈 맵", func(t *testing.T) {
		seedSnapshotPerf(t, ctx, r, 1, 1)
		if _, err := r.pool.Exec(ctx, `DELETE FROM threshold_snapshots`); err != nil {
			t.Fatalf("비우기: %v", err)
		}
		got, err := r.lastThresholdSnapshots(ctx)
		if err != nil {
			t.Fatalf("lastThresholdSnapshots: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("빈 테이블 — %d키, want 0", len(got))
		}
	})
}
