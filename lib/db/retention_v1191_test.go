package db

import (
	"context"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func TestRetentionCutoffDaysClamp(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	clamp := now.AddDate(0, 0, -maxRetentionDays)
	cases := []struct {
		name    string
		days    int
		def     int
		wantCut time.Time
	}{
		{"정상 90일", 90, 30, now.AddDate(0, 0, -90)},
		{"0 — 기본값 폴백", 0, 30, now.AddDate(0, 0, -30)},
		{"음수 — 기본값 폴백", -5, 30, now.AddDate(0, 0, -30)},
		{"상한 딱 맞음 106751", 106751, 30, clamp},
		{"상한+1 106752 — 랩 직전 클램프", 106752, 30, clamp},
		{"200000 — 클램프", 200000, 30, clamp},
		{"9999999 — 클램프", 9999999, 30, clamp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := retentionCutoffDays(tc.days, tc.def, now)
			if !got.Equal(tc.wantCut) {
				t.Fatalf("retentionCutoffDays(%d, %d) = %v, want %v", tc.days, tc.def, got, tc.wantCut)
			}
			if got.After(now) {
				t.Fatalf("cutoff %v — 미래(전면 삭제 랩 결함)", got)
			}
		})
	}
}

func TestGateStampDueFutureStamps(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	future := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	nearFuture := now.Add(time.Hour).Format(time.RFC3339)
	for name, fn := range map[string]func(string, time.Time) bool{
		"snapshotGateDue": snapshotGateDue,
		"rollupGateDue":   rollupGateDue,
		"pruneGateDue":    pruneGateDue,
	} {
		for _, stamp := range []struct {
			label string
			val   string
		}{
			{"2999년 스탬프", future},
			{"1h 미래 스탬프", nearFuture},
		} {
			t.Run(name+"·"+stamp.label, func(t *testing.T) {
				if !fn(stamp.val, now) {
					t.Fatalf("%s(%q) = false, want true(미래 스탬프 = 즉시 만료)", name, stamp.val)
				}
			})
		}
	}
}

func setRetentionOverride(t *testing.T, r *Repo, key string, days int) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, strconv.Itoa(days)); err != nil {
		t.Fatalf("오버라이드 %s: %v", key, err)
	}
}

func TestPruneThresholdSnapshotsOverflowOverride(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	setRetentionOverride(t, r, "threshold_snapshots_retention_days", 200000)
	old := time.Now().UTC().AddDate(0, 0, -100)
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO threshold_snapshots (ts, key, old_value, new_value)
		 VALUES ($1, 'pending_max', '10', '20')`, old); err != nil {
		t.Fatalf("대상 행 적립: %v", err)
	}
	if err := r.pruneThresholdSnapshots(ctx); err != nil {
		t.Fatalf("pruneThresholdSnapshots: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM threshold_snapshots WHERE key = 'pending_max' AND old_value = '10'`).
		Scan(&n); err != nil {
		t.Fatalf("잔존 카운트: %v", err)
	}
	if n != 1 {
		t.Fatalf("오버플로 오버라이드 200000일에서 행 %d건, want 1(전면 삭제 랩 결함 재현)", n)
	}
}

func TestPruneReadEventsOverflowOverride(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	setRetentionOverride(t, r, "events_retention_days", 9999999)
	old := time.Now().UTC().AddDate(0, 0, -200)
	for i := 0; i < 3; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (ts, hits, zero, near, fallback, session, ids, first_rank, bytes, zero_reason, source, near_score)
			 VALUES ($1, 1, false, false, false, 'overflow-test', '', 0, 0, '', 'cli', 0)`, old); err != nil {
			t.Fatalf("대상 행 적립: %v", err)
		}
	}
	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = 'overflow-test'`).Scan(&n); err != nil {
		t.Fatalf("잔존 카운트: %v", err)
	}
	if n != 3 {
		t.Fatalf("오버플로 오버라이드 9999999일에서 %d행, want 3(전면 삭제 랩 결함 재현)", n)
	}
}

func TestClaimThresholdSnapshotGateSingleWinner(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	const n = 4

	claimBurst := func(last string) int32 {
		var winners atomic.Int32
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		ready.Add(n)
		done.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				if r.claimThresholdSnapshotGate(ctx, last) != "" {
					winners.Add(1)
				}
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		return winners.Load()
	}

	t.Run("부재 행 INSERT 경로", func(t *testing.T) {
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM meta WHERE key = $1`, thresholdSnapshotGateKey); err != nil {
			t.Fatalf("게이트 키 삭제: %v", err)
		}
		if got := claimBurst(""); got != 1 {
			t.Fatalf("부재 행 선점 승자 %d명, want 1", got)
		}
	})

	t.Run("존재 행 UPDATE 경로", func(t *testing.T) {
		old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			thresholdSnapshotGateKey, old); err != nil {
			t.Fatalf("게이트 키 설정: %v", err)
		}
		if got := claimBurst(old); got != 1 {
			t.Fatalf("존재 행 선점 승자 %d명, want 1", got)
		}
	})
}

func TestSnapshotSeedingSinglePassUnderConcurrency(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	saved := snapshotGateCheckedAt.Load()
	t.Cleanup(func() { snapshotGateCheckedAt.Store(saved) })

	const n = 4
	const rounds = 5
	want := len(wire.ThresholdsKeys())
	for round := 0; round < rounds; round++ {
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM meta WHERE key = $1`, thresholdSnapshotGateKey); err != nil {
			t.Fatalf("게이트 키 삭제: %v", err)
		}
		if _, err := r.pool.Exec(ctx, `DELETE FROM threshold_snapshots`); err != nil {
			t.Fatalf("스냅숏 초기화: %v", err)
		}
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		ready.Add(n)
		done.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer done.Done()
				snapshotGateCheckedAt.Store(0)
				ready.Done()
				<-start
				r.maybeSnapshotThresholds(ctx)
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()

		var got int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM threshold_snapshots`).Scan(&got); err != nil {
			t.Fatalf("시딩 행 카운트: %v", err)
		}
		if got != want {
			t.Fatalf("round %d: 병렬 첫 시딩 %d행, want %d(전체 키 1세트 — 이중 시딩 결함 재현)", round, got, want)
		}
	}
}

func TestLastThresholdSnapshotsLatestPerKey(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `DELETE FROM threshold_snapshots`); err != nil {
		t.Fatalf("스냅숏 초기화: %v", err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	for _, row := range []struct {
		ts   time.Time
		key  string
		newV string
	}{
		{base, "pending_max", "10"},
		{base, "dup_susp_max", "3"},
		{base.Add(time.Hour), "pending_max", "20"},
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO threshold_snapshots (ts, key, old_value, new_value)
			 VALUES ($1, $2, '', $3)`, row.ts, row.key, row.newV); err != nil {
			t.Fatalf("시퀀스 행 적립: %v", err)
		}
	}
	got, err := r.lastThresholdSnapshots(ctx)
	if err != nil {
		t.Fatalf("lastThresholdSnapshots: %v", err)
	}
	if got["pending_max"] != "20" {
		t.Fatalf("pending_max 최신값 = %q, want \"20\"(키별 최신 판정)", got["pending_max"])
	}
	if got["dup_susp_max"] != "3" {
		t.Fatalf("dup_susp_max = %q, want \"3\"", got["dup_susp_max"])
	}
}

func TestRecordUsageCountOversizedQuiet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	if err := r.RecordUsageCount(ctx, "hot", "hook", math.MaxInt64); err != nil {
		t.Fatalf("MaxInt64 count = %v, want nil(조용한 거부)", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = $1`, "daily:hot:"+today).Scan(&n); err != nil {
		t.Fatalf("flat 키 카운트: %v", err)
	}
	if n != 0 {
		t.Fatalf("MaxInt64 기록 후 daily:hot:%s 존재, want 부재(미수용)", today)
	}
	if err := r.RecordUsageCount(ctx, "hot", "hook", 1); err != nil {
		t.Fatalf("후속 정상 기록: %v", err)
	}
	if got := flatDailyValue(t, r, "hot", today); got != 1 {
		t.Fatalf("후속 증분 = %d, want 1", got)
	}
}

func TestRecordReadEventNearScoreClampRoundtrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cases := []struct {
		in   float64
		want float64
	}{
		{math.NaN(), 0},
		{math.Inf(1), 0},
		{math.Inf(-1), 0},
		{-0.5, 0},
		{1.5, 1},
		{0.25, 0.25},
	}
	for i, tc := range cases {
		if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 0, Zero: true, Near: false, Session: "clamp-test", NearScore: tc.in}); err != nil {
			t.Fatalf("RecordReadEvent[%d]: %v", i, err)
		}
	}
	rows, err := r.pool.Query(ctx,
		`SELECT near_score FROM read_events WHERE session = 'clamp-test' ORDER BY ts ASC, near_score ASC`)
	if err != nil {
		t.Fatalf("near_score 조회: %v", err)
	}
	defer rows.Close()
	var got []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("near_score scan: %v", err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("near_score rows: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("행 수 %d, want %d", len(got), len(cases))
	}

	wantSet := map[float64]int{}
	for _, tc := range cases {
		wantSet[tc.want]++
	}
	gotSet := map[float64]int{}
	for _, v := range got {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("클램프 위반 값 저장됨: %v", v)
		}
		gotSet[v]++
	}
	for want, c := range wantSet {
		if gotSet[want] != c {
			t.Fatalf("값 %v 저장 %d회, want %d회(전체 왕복: got %v)", want, gotSet[want], c, got)
		}
	}
}
