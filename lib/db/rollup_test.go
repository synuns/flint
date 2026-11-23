// rollup_test.go — the weekly-rollup circuit tests (schema v13 A3):
// the UTC Monday week boundary, the 24h meta gate + 1h process cache,
// num/den aggregation over last week's raw read_events (the report.go
// EffectOutput definitions — den = in-window reads), UPSERT
// idempotence, the empty-week skip, retention, quiet failure, and the
// hook-order contract (rollup precedes the prune). Slow bodies run on
// a real DB (synthetic fixtures only — the no-personal-data principle).
package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// resetRollupGateCache — zeroes the in-process gate-check cache (the
// snapshotGate twin).
func resetRollupGateCache(t *testing.T) {
	t.Helper()
	saved := rollupGateCheckedAt.Load()
	rollupGateCheckedAt.Store(0)
	t.Cleanup(func() { rollupGateCheckedAt.Store(saved) })
}

// setRollupGate — writes the gate meta key directly (zero time deletes
// it — the never-run state).
func setRollupGate(t *testing.T, r *Repo, at time.Time) {
	t.Helper()
	if at.IsZero() {
		if _, err := r.pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1`, rollupGateKey); err != nil {
			t.Fatalf("게이트 키 삭제: %v", err)
		}
		return
	}
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		rollupGateKey, at.Format(time.RFC3339)); err != nil {
		t.Fatalf("게이트 키 설정: %v", err)
	}
}

// TestLastWeekStart — the week-boundary pure function: UTC Monday
// midnight starting the fully-elapsed week before the current one. A
// Wednesday maps back 9 days (2 into the current week + 7); a Monday
// maps back exactly 7 (the current week's Monday is NOT elapsed); a
// Sunday sits 6 days into the ISO week.
func TestLastWeekStart(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"수요일", time.Date(2026, 11, 18, 15, 4, 5, 0, time.UTC), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)},
		{"월요일 00시 직후 — 당일 주는 미경과", time.Date(2026, 11, 16, 0, 30, 0, 0, time.UTC), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)},
		{"월요일 23시", time.Date(2026, 11, 16, 23, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)},
		{"일요일 — 당일 주(11/16~)는 미경과", time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)},
		{"비UTC 입력 — UTC 정규화", time.Date(2026, 11, 18, 15, 0, 0, 0, time.FixedZone("KST", 9*3600)), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastWeekStart(tc.now); !got.Equal(tc.want) {
				t.Fatalf("lastWeekStart(%v) = %v, want %v(UTC 월요일 자정)", tc.now, got, tc.want)
			}
		})
	}
}

// TestRollupGateDueProbe — the gate pure functions (the snapshotGate
// twins): within-24h stamps are not due; the 1h process cache holds.
func TestRollupGateDueProbe(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if !rollupGateDue("", now) || !rollupGateDue("bad", now) {
		t.Fatal("미기록·비RFC3339 — 만료여야 함")
	}
	if rollupGateDue(now.Add(-23*time.Hour).Format(time.RFC3339), now) {
		t.Fatal("23h 경과 — 미만료여야 함")
	}
	if !rollupGateDue(now.Add(-25*time.Hour).Format(time.RFC3339), now) {
		t.Fatal("25h 경과 — 만료여야 함")
	}
	resetRollupGateCache(t)
	if !rollupGateProbe(now) {
		t.Fatal("첫 probe = false, want true")
	}
	if rollupGateProbe(now.Add(30 * time.Minute)) {
		t.Fatal("TTL 내 재probe = true, want false(1h 프로세스 캐시)")
	}
}

// TestReadHookOrderGuard — the RecordReadEvent tail hook order as a
// source contract: the rollup check precedes the prune (aggregate then
// clean — a week's raw events are never deleted before the rollup
// confirms), which precedes the snapshot hook.
func TestReadHookOrderGuard(t *testing.T) {
	b, err := os.ReadFile("usage.go")
	if err != nil {
		t.Fatalf("usage.go 읽기: %v", err)
	}
	src := string(b)
	rollupAt := strings.Index(src, "r.maybeRollupWeekly(ctx)")
	pruneAt := strings.Index(src, "r.maybePruneAfterRead(ctx)")
	snapAt := strings.Index(src, "r.maybeSnapshotThresholds(ctx)")
	if rollupAt < 0 || pruneAt < 0 || snapAt < 0 {
		t.Fatalf("훅 잔여 불완전 — rollup:%d prune:%d snapshot:%d(RecordReadEvent 꼬리 3훅)", rollupAt, pruneAt, snapAt)
	}
	if !(rollupAt < pruneAt && pruneAt < snapAt) {
		t.Fatalf("훅 순서 위반 — rollup:%d prune:%d snapshot:%d(롤오버가 PruneReadEvents에 선행 필요)", rollupAt, pruneAt, snapAt)
	}
}

// rollupFixture — seeds last week's synthetic events (Monday-based ws)
// plus out-of-window guards: den=3, zero=1, fallback=1, pairs=1 (a
// knowledge write 10m after e1 pairs; the 45m write after e3 does
// not — the report pair_window definition).
func rollupFixture(t *testing.T, r *Repo) time.Time {
	t.Helper()
	ctx := context.Background()
	ws := lastWeekStart(time.Now().UTC())
	for _, ev := range []struct {
		off      time.Duration
		zero     bool
		fallback bool
	}{
		{1 * time.Hour, false, false},
		{2 * time.Hour, true, false},
		{3 * time.Hour, false, true},
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (ts, hits, zero, near, fallback) VALUES ($1, 1, $2, false, $3)`,
			ws.Add(ev.off), ev.zero, ev.fallback); err != nil {
			t.Fatalf("합성 이벤트 적립: %v", err)
		}
	}
	// A paired write — 10m after e1 (inside the 30m pair window).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash, ingested_at)
		 VALUES ('rollup-pair', '합성 데모', ':a:', 0.9, 'rollup-pair-1', $1)`,
		ws.Add(1*time.Hour+10*time.Minute)); err != nil {
		t.Fatalf("페어 write 적립: %v", err)
	}
	// An unpaired write — 45m after e3 (outside the window).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash, ingested_at)
		 VALUES ('rollup-late', '합성 데모', ':a:', 0.9, 'rollup-late-1', $1)`,
		ws.Add(3*time.Hour+45*time.Minute)); err != nil {
		t.Fatalf("비페어 write 적립: %v", err)
	}
	// Out-of-window guards — the current week and 2 weeks ago never
	// enter the aggregation (the target is last week only, no backfill).
	// The current-week point is derived from the week boundary (ws+7d+1h):
	// a naive "now-1h" lands in LAST week just after midnight UTC on
	// Monday, corrupting the fixture (observed 2026-11-23 CI).
	for _, ts := range []time.Time{
		ws.Add(7*24*time.Hour + 1*time.Hour),
		ws.Add(-1 * time.Hour),
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (ts, hits, zero, near, fallback) VALUES ($1, 1, true, false, true)`, ts); err != nil {
			t.Fatalf("창 외 이벤트 적립: %v", err)
		}
	}
	return ws
}

// rollupRow — one (num, den) per metric, absent metrics report (-1, -1).
func rollupRow(t *testing.T, r *Repo, ws time.Time, metric string) (int64, int64) {
	t.Helper()
	var num, den int64
	err := r.pool.QueryRow(context.Background(),
		`SELECT num, den FROM weekly_rollups WHERE week_start = $1 AND metric = $2`, ws, metric).
		Scan(&num, &den)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return -1, -1
		}
		t.Fatalf("rollup 행 조회(%s): %v", metric, err)
	}
	return num, den
}

// TestWeeklyRollupAggregation — the full circuit from the
// RecordReadEvent hook: last week's num/den land per metric (report's
// definitions — den = in-window reads), the forced rerun re-aggregates
// to identical values (UPSERT idempotence), and a mid-week value change
// of the raw source converges on the next pass.
func TestWeeklyRollupAggregation(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetRollupGateCache(t)
	ws := rollupFixture(t, r)

	// The hook chain from RecordReadEvent (never-run gate → first pass).
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	for _, m := range []string{"zero_pct", "fallback_pct", "pair_pct"} {
		num, den := rollupRow(t, r, ws, m)
		if num != 1 || den != 3 {
			t.Fatalf("%s = (%d,%d), want (1,3)(report 산식 — den=창 내 read 수)", m, num, den)
		}
	}
	// The gate stamped.
	var gate string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, rollupGateKey).Scan(&gate); err != nil {
		t.Fatalf("게이트 키 조회: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, gate); err != nil {
		t.Fatalf("게이트 스탬프 %q — RFC3339 아님", gate)
	}

	// Idempotent rerun — same values, still 3 rows.
	setRollupGate(t, r, time.Now().UTC().Add(-25*time.Hour))
	resetRollupGateCache(t)
	r.maybeRollupWeekly(ctx)
	var rows int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM weekly_rollups`).Scan(&rows); err != nil {
		t.Fatalf("행 수 조회: %v", err)
	}
	if rows != 3 {
		t.Fatalf("재집계 후 %d행, want 3(UPSERT 멱등)", rows)
	}
	for _, metric := range []string{"zero_pct", "fallback_pct", "pair_pct"} {
		num, den := rollupRow(t, r, ws, metric)
		if num != 1 || den != 3 {
			t.Fatalf("재집계 %s = (%d,%d), want (1,3)", metric, num, den)
		}
	}

	// A late-arriving raw event converges on the next pass (the daily
	// re-aggregation self-heals late writes).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (ts, hits, zero, near, fallback) VALUES ($1, 1, true, false, false)`,
		ws.Add(4*time.Hour)); err != nil {
		t.Fatalf("지연 이벤트 적립: %v", err)
	}
	setRollupGate(t, r, time.Now().UTC().Add(-25*time.Hour))
	resetRollupGateCache(t)
	r.maybeRollupWeekly(ctx)
	if num, den := rollupRow(t, r, ws, "zero_pct"); num != 2 || den != 4 {
		t.Fatalf("지연 반영 zero_pct = (%d,%d), want (2,4)", num, den)
	}
	if num, den := rollupRow(t, r, ws, "pair_pct"); num != 1 || den != 4 {
		t.Fatalf("지연 반영 pair_pct = (%d,%d), want (1,4)", num, den)
	}
}

// TestWeeklyRollupEmptyWeek — a week with 0 read events writes no row
// (the den=0 skip — a 0/0 ratio is meaningless; the week stays absent,
// stated explicitly).
func TestWeeklyRollupEmptyWeek(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetRollupGateCache(t)
	r.maybeRollupWeekly(ctx)
	var rows int64
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM weekly_rollups`).Scan(&rows); err != nil {
		t.Fatalf("행 수 조회: %v", err)
	}
	if rows != 0 {
		t.Fatalf("빈 주 %d행, want 0(빈 주 스킵)", rows)
	}
	// The gate still stamps on the successful (skipped-aggregate) pass —
	// an empty week is a completed rollover, not a failure.
	var gate string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = $1`, rollupGateKey).Scan(&gate); err != nil {
		t.Fatalf("게이트 키 조회: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, gate); err != nil {
		t.Fatalf("게이트 스탬프 %q — 빈 주도 성공 스탬프", gate)
	}
}

// TestWeeklyRollupRetention — rollup rows past
// weekly_rollups_retention_days (default 90) are deleted on the pass;
// fresh rows stay.
func TestWeeklyRollupRetention(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetRollupGateCache(t)
	ws := lastWeekStart(time.Now().UTC())
	old := ws.Add(-100 * 24 * time.Hour) // ~14 weeks back — past the 90d default retention
	for _, w := range []struct {
		start time.Time
		hash  string
	}{
		{old, "expired"},
		{ws, "fresh"},
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO weekly_rollups (week_start, metric, num, den)
			 VALUES ($1, 'zero_pct', 1, 2)`, w.start); err != nil {
			t.Fatalf("rollup 행 적립(%s): %v", w.hash, err)
		}
	}
	r.maybeRollupWeekly(ctx)
	var expired, fresh int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM weekly_rollups WHERE week_start = $1`, old).Scan(&expired); err != nil {
		t.Fatalf("만료 행 카운트: %v", err)
	}
	if expired != 0 {
		t.Fatalf("만료 행 잔존 %d건, want 0(retention 삭제)", expired)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM weekly_rollups WHERE week_start = $1`, ws).Scan(&fresh); err != nil {
		t.Fatalf("최근 행 카운트: %v", err)
	}
	if fresh != 1 {
		t.Fatalf("최근 주 행 = %d건, want 1(retention은 만료 행만 삭제 — 최근 보존)", fresh)
	}
}

// TestWeeklyRollupQuietFailure — a mid-circuit failure (the table
// missing) propagates nothing: no panic, no error, and the DB gate
// stays unstamped so the next pass retries.
func TestWeeklyRollupQuietFailure(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetRollupGateCache(t)
	if _, err := r.pool.Exec(ctx, `DROP TABLE weekly_rollups`); err != nil {
		t.Fatalf("테이블 삭제: %v", err)
	}
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("패닉 발생: %v", rec)
		}
	}()
	r.maybeRollupWeekly(ctx)
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = $1 AND value <> ''`, rollupGateKey).
		Scan(&n); err != nil {
		t.Fatalf("게이트 키 조회: %v", err)
	}
	if n != 0 {
		t.Fatal("실패 후 게이트 스탬프 — 재시도 하루 차단(stamp-then-fail 위반)")
	}
}
