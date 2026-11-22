// autotune_shadow_test.go — the autotune shadow-circuit tests (v1.23.0
// schema v18, spec B2): the 24h last_shadow_at gate, the off-default zero
// steady-state cost, trigger fire/no-fire, the prediction-row full-field
// contract, the 5-case streak lifecycle, off→shadow state persistence,
// the safe-range proposal invariant, the ≤5 round-trip budget
// (counter-injected), and the read-tail hook order guard. Slow bodies run
// on a real DB (synthetic scratch only — the no-personal-data principle).
// The circuit is registered with a synthetic fixture rule by default; the
// real ops registry conformance runs in autotune_shadow_real_test.go
// (package db_test) under SUPERLITE_REAL=1 — the swappable-seam contract.
package db

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/synuns/flint/lib/store"
)

// fakeShadowRule — the synthetic fixture rule (the registry's SHAPE, not
// its source: the verdict query scans the same synthetic read_events
// distribution the tests seed). The real registry is exercised by the
// external SUPERLITE_REAL=1 conformance file.
func fakeShadowRule() ShadowRule {
	return ShadowRule{
		Signal: "auto_zero_near_high",
		// The portable aggregate form — Doltgres rejects COUNT(*) FILTER
		// ("function filters are not yet supported", SQLSTATE XX000 —
		// measured on the first real-DB execution; incident logged). The
		// reclass arm carries the near_score domain guard (r101-2), mirroring
		// the registry shape.
		Query: `SELECT COUNT(*) AS sample_n,
       COUNT(CASE WHEN zero THEN 1 END) AS zero_n,
       COUNT(CASE WHEN zero AND near_score BETWEEN 0 AND 1 AND near_score >= $1 THEN 1 END) AS reclass_n
  FROM read_events
 WHERE ts >= now() - interval '14 days'`,
		Key:       "suggest_min_score",
		Direction: "down",
		SafeRange: [2]float64{0.05, 0.30},
		Auto:      "shadow",
		SuccessPair: "zero_near_pct decreases with hit_pct and the read count unchanged " +
			"(a hit is round-over-round stability, not causality)",
		ObsWindowD: 14,
	}
}

// fakeShadowVerdict — the fixture verdict fn (the injected seam's fake):
// the sample floor and warn ratio read from the injected thresholds with
// the default fallback (0 → the registry defaults), mirroring only the
// contract the tests rely on.
func fakeShadowVerdict(rule ShadowRule, obs ShadowObs, th store.Thresholds) ShadowVerdict {
	def := store.DefaultThresholds()
	if th.ZeroNearMinRead <= 0 {
		th.ZeroNearMinRead = def.ZeroNearMinRead
	}
	if th.ZeroNearWarnPct <= 0 {
		th.ZeroNearWarnPct = def.ZeroNearWarnPct
	}
	v := ShadowVerdict{SampleOk: obs.ReadEventsN >= th.ZeroNearMinRead}
	if obs.ZeroN > 0 {
		v.Ratio = obs.ReclassN * 100 / obs.ZeroN
	}
	if !v.SampleOk {
		v.Detail = "sample below floor"
		return v
	}
	v.Fired = v.Ratio >= th.ZeroNearWarnPct
	v.Detail = "fixture verdict"
	return v
}

// registerFakeShadow — installs the fixture circuit and restores the
// unregistered (nil) state afterwards (the seam is package-global — no
// other test observes the fixture).
func registerFakeShadow(t *testing.T) {
	t.Helper()
	RegisterShadowCircuit(func() []ShadowRule { return []ShadowRule{fakeShadowRule()} }, fakeShadowVerdict)
	t.Cleanup(func() { RegisterShadowCircuit(nil, nil) })
}

// resetShadowGateCache — zeroes the in-process shadow gate-check cache so
// a test observes the gate DB read (restored via cleanup).
func resetShadowGateCache(t *testing.T) {
	t.Helper()
	saved := shadowGateCheckedAt.Load()
	shadowGateCheckedAt.Store(0)
	t.Cleanup(func() { shadowGateCheckedAt.Store(saved) })
}

// warmShadowGateCache — stamps the cache to now (probe returns false —
// the steady-state hot path).
func warmShadowGateCache() {
	shadowGateCheckedAt.Store(time.Now().UTC().Unix())
}

// countShadowTrips — swaps the round-trip execution seams for counting
// wrappers (delegating to the originals). Returns bump functions reading
// the live counts; the originals restore on cleanup.
func countShadowTrips(t *testing.T) (rowTrips func() int, execTrips func() int) {
	t.Helper()
	rowFn, execFn := shadowQueryRowFn, shadowExecFn
	var rows, execs int
	shadowQueryRowFn = func(ctx context.Context, r *Repo, query string, args ...any) pgx.Row {
		rows++
		return rowFn(ctx, r, query, args...)
	}
	shadowExecFn = func(ctx context.Context, r *Repo, query string, args ...any) error {
		execs++
		return execFn(ctx, r, query, args...)
	}
	t.Cleanup(func() { shadowQueryRowFn, shadowExecFn = rowFn, execFn })
	return func() int { return rows }, func() int { return execs }
}

// seedShadowReads — seeds the synthetic window distribution: hit rows,
// zero rows below the fixture floor (near_score 0.01), and zero rows at
// 0.5 (>= the 0.05 floor — the reclass pool).
func seedShadowReads(t *testing.T, r *Repo, hits, zeroLow, zeroNear int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < hits; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (hits, zero, near, fallback) VALUES (2, false, false, false)`); err != nil {
			t.Fatalf("시딩 hit 행: %v", err)
		}
	}
	for i := 0; i < zeroLow; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (hits, zero, near, fallback, near_score) VALUES (0, true, false, false, 0.01)`); err != nil {
			t.Fatalf("시딩 zero-low 행: %v", err)
		}
	}
	for i := 0; i < zeroNear; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (hits, zero, near, fallback, near_score) VALUES (0, true, false, false, 0.5)`); err != nil {
			t.Fatalf("시딩 zero-near 행: %v", err)
		}
	}
}

// setShadowMeta — upserts one meta key (empty value = delete).
func setShadowMeta(t *testing.T, r *Repo, key, val string) {
	t.Helper()
	if val == "" {
		if _, err := r.pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1`, key); err != nil {
			t.Fatalf("meta %s 삭제: %v", key, err)
		}
		return
	}
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, val); err != nil {
		t.Fatalf("meta %s 설정: %v", key, err)
	}
}

// shadowMetaVal — one meta key's value ("" when absent).
func shadowMetaVal(t *testing.T, r *Repo, key string) string {
	t.Helper()
	var val string
	err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = $1`, key).Scan(&val)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return ""
		}
		t.Fatalf("meta %s 조회: %v", key, err)
	}
	return val
}

// shadowPredictionRow — the latest tuning_predictions row for a key.
type shadowPredictionRow struct {
	trigger, key, cur, newV, rationale string
	predPct, reclassN, sampleN         int
}

// shadowPredictions — all prediction rows for a key, ts-ascending.
func shadowPredictions(t *testing.T, r *Repo, key string) []shadowPredictionRow {
	t.Helper()
	rows, err := r.pool.Query(context.Background(),
		`SELECT trigger, key, cur, new, pred_zero_near_pct, pred_reclass_n, sample_n, rationale
		   FROM tuning_predictions WHERE key = $1 ORDER BY ts ASC, id ASC`, key)
	if err != nil {
		t.Fatalf("예측 행 조회: %v", err)
	}
	defer rows.Close()
	var out []shadowPredictionRow
	for rows.Next() {
		var p shadowPredictionRow
		if err := rows.Scan(&p.trigger, &p.key, &p.cur, &p.newV, &p.predPct, &p.reclassN, &p.sampleN, &p.rationale); err != nil {
			t.Fatalf("예측 행 scan: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// openShadowGate — backdates the 24h gate stamp (the setSnapshotGate
// convention — a completed round stamps now(), so consecutive rounds in
// one test must reopen the DB gate).
func openShadowGate(t *testing.T, r *Repo) {
	t.Helper()
	setShadowMeta(t, r, "last_shadow_at", time.Now().UTC().Add(-25*time.Hour).Format(time.RFC3339))
}

// runShadowRound — resets the gate cache and runs one shadow round.
func runShadowRound(t *testing.T, r *Repo) {
	t.Helper()
	resetShadowGateCache(t)
	r.maybeShadowRound(context.Background())
}

// fireRound — opens the DB gate, then runs one shadow round (the test
// body of a consecutive-rounds scenario).
func fireRound(t *testing.T, r *Repo) {
	t.Helper()
	openShadowGate(t, r)
	runShadowRound(t, r)
}

// TestShadowGateProbeCache — the 1h in-process gate cache (the
// pruneGateProbe/snapshotGateProbe twin): first check true, within TTL
// false, after TTL true again.
func TestShadowGateProbeCache(t *testing.T) {
	saved := shadowGateCheckedAt.Load()
	shadowGateCheckedAt.Store(0)
	t.Cleanup(func() { shadowGateCheckedAt.Store(saved) })
	now := time.Now().UTC()
	if !shadowGateProbe(now) {
		t.Fatal("첫 probe = false, want true(미확인 상태)")
	}
	if shadowGateProbe(now.Add(30 * time.Minute)) {
		t.Fatal("TTL 내 재probe = true, want false(1h 프로세스 캐시)")
	}
	if !shadowGateProbe(now.Add(2 * time.Hour)) {
		t.Fatal("TTL 경과 후 probe = false, want true(재평가)")
	}
}

// TestShadowProposalPure — the proposal pure function: the safe-range
// bound in the registry direction, a strict move only when cur allows
// one, and the proposal always inside the closed safe range.
func TestShadowProposalPure(t *testing.T) {
	down := fakeShadowRule()
	cases := []struct {
		name      string
		direction string
		safe      [2]float64
		cur       float64
		wantNew   float64
		wantOK    bool
		inRange   bool // implied — asserted separately
	}{
		{"down — cur above lo", "down", [2]float64{0.05, 0.30}, 0.15, 0.05, true, true},
		{"down — cur at lo(이동 없음)", "down", [2]float64{0.05, 0.30}, 0.05, 0.05, false, true},
		{"down — cur below lo(범위 회복 상승)", "down", [2]float64{0.05, 0.30}, 0.02, 0.05, true, true},
		{"down — cur above hi", "down", [2]float64{0.05, 0.30}, 0.40, 0.05, true, true},
		{"up — cur below hi", "up", [2]float64{0.05, 0.30}, 0.10, 0.30, true, true},
		{"up — cur at hi(이동 없음)", "up", [2]float64{0.05, 0.30}, 0.30, 0.30, false, true},
		{"up — cur above hi(범위 회복 하강)", "up", [2]float64{0.05, 0.30}, 0.35, 0.30, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := down
			rule.Direction = tc.direction
			rule.SafeRange = tc.safe
			got, ok := shadowProposal(rule, tc.cur)
			if got != tc.wantNew || ok != tc.wantOK {
				t.Fatalf("shadowProposal(%s, %v) = (%v, %v), want (%v, %v)",
					tc.direction, tc.cur, got, ok, tc.wantNew, tc.wantOK)
			}
			if got < rule.SafeRange[0] || got > rule.SafeRange[1] {
				t.Fatalf("제안값 %v — 안전범위 [%v, %v] 밖", got, rule.SafeRange[0], rule.SafeRange[1])
			}
		})
	}
}

// TestShadowStreakNextPure — the 5 streak rules as the pure transition:
// first fire = 1, direction maintained = +1 (value change irrelevant),
// non-fired sample-ok round = maintain, direction reversal = reset to 1,
// sample-floor miss = reset to 0.
func TestShadowStreakNextPure(t *testing.T) {
	cases := []struct {
		name                string
		prevStreak, prevDir int
		fired, sampleOk     bool
		thisDir             int
		want                int
	}{
		{"첫 발화 — 이전 예측 없음", 0, 0, true, true, -1, 1},
		{"값 변화·방향 유지 = 적중", 2, -1, true, true, -1, 3},
		{"미발화 라운드 — 유지", 2, -1, false, true, 0, 2},
		{"방향 반전 — 리셋 후 1", 3, -1, true, true, 1, 1},
		{"표본 미달 — 0 리셋", 3, -1, false, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shadowStreakNext(tc.prevStreak, tc.prevDir, tc.thisDir, tc.fired, tc.sampleOk); got != tc.want {
				t.Fatalf("shadowStreakNext(%d, %d, %d, %v, %v) = %d, want %d",
					tc.prevStreak, tc.prevDir, tc.thisDir, tc.fired, tc.sampleOk, got, tc.want)
			}
		})
	}
}

// TestReadHookOrderGuardShadow — the RecordReadEvent tail hook order as a
// source contract: rollup → prune → snapshot → shadow (the shadow round
// observes the post-cleanup state; the v1.23.0 4th hook).
func TestReadHookOrderGuardShadow(t *testing.T) {
	b, err := os.ReadFile("usage.go")
	if err != nil {
		t.Fatalf("usage.go 읽기: %v", err)
	}
	src := string(b)
	at := func(call string) int {
		i := strings.Index(src, call)
		if i < 0 {
			t.Fatalf("훅 %s 잔여 없음(RecordReadEvent 꼬리 4훅)", call)
		}
		return i
	}
	rollupAt := at("r.maybeRollupWeekly(ctx)")
	pruneAt := at("r.maybePruneAfterRead(ctx)")
	snapAt := at("r.maybeSnapshotThresholds(ctx)")
	shadowAt := at("r.maybeShadowRound(ctx)")
	if !(rollupAt < pruneAt && pruneAt < snapAt && snapAt < shadowAt) {
		t.Fatalf("훅 순서 위반 — rollup:%d prune:%d snapshot:%d shadow:%d(shadow는 정리 후 관측)", rollupAt, pruneAt, snapAt, shadowAt)
	}
}

// TestShadowOffZeroCost — the unconfigured deployment pays nothing: with
// the circuit registered but auto_mode at its off default, one cold read
// costs exactly the single batched gate+mode SELECT (no writes, no
// prediction), and the steady state (warm 1h cache) costs 0 round trips.
func TestShadowOffZeroCost(t *testing.T) {
	registerFakeShadow(t)
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	seedShadowReads(t, r, 11, 5, 1) // fire-shaped data — mode off must suppress it

	runShadowRound(t, r)
	if got := rowTrips() + execTrips(); got != 1 {
		t.Fatalf("off 콜드 라운드 왕복 %d, want 1(게이트+모드 단일 배치)", got)
	}
	if p := shadowPredictions(t, r, "suggest_min_score"); len(p) != 0 {
		t.Fatalf("off 상태 예측 행 %d건, want 0", len(p))
	}
	for _, key := range []string{"last_shadow_at", "fired_auto_auto_zero_near_high", "auto_streak_suggest_min_score", "auto_rounds_suggest_min_score"} {
		if v := shadowMetaVal(t, r, key); v != "" {
			t.Fatalf("off 상태 meta %s = %q, want 미기록", key, v)
		}
	}

	// Corrupt mode text behaves as off (quiet skip — the enum default).
	setShadowMeta(t, r, "auto_mode", "bogus")
	runShadowRound(t, r)
	if got := rowTrips() + execTrips(); got != 2 { // 1 (previous round) + 1 (this round's gate read)
		t.Fatalf("mode 파손 라운드 왕복 누적 %d, want 2", got)
	}
	if v := shadowMetaVal(t, r, "last_shadow_at"); v != "" {
		t.Fatalf("mode 파손 시 last_shadow_at = %q, want 미기록", v)
	}

	// Warm cache — 0 round trips on the hot read path.
	setShadowMeta(t, r, "auto_mode", "")
	warmShadowGateCache()
	r.maybeShadowRound(context.Background())
	if got := rowTrips() + execTrips(); got != 2 {
		t.Fatalf("캐시 내 라운드 왕복 누적 %d, want 2(추가 0 — steady state 비용 0)", got)
	}
}

// TestShadowGate24h — a shadow round stamps last_shadow_at; within 24h
// the DB gate closes the round (one gate read, nothing else) even with
// the process cache reset.
func TestShadowGate24h(t *testing.T) {
	registerFakeShadow(t)
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 11, 1, 5) // sample 17, ratio 83% — fires

	fireRound(t, r)
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 1 {
		t.Fatalf("발화 라운드 예측 행 %d건, want 1", n)
	}
	gate := shadowMetaVal(t, r, "last_shadow_at")
	if _, err := time.Parse(time.RFC3339, gate); err != nil {
		t.Fatalf("게이트 스탬프 %q — RFC3339 아님", gate)
	}

	// Within 24h — closed (the fresh stamp, no backdate).
	runShadowRound(t, r)
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 1 {
		t.Fatalf("게이트 폐쇄 중 예측 행 %d건, want 1(불변)", n)
	}
	if got := rowTrips(); got != 4 { // round 1: gate+verdict+what-if (3 rows) + round 2 gate read
		t.Fatalf("row 왕복 누적 %d, want 4(폐쇄 라운드는 게이트 읽기만)", got)
	}
	if execTrips() != 2 { // only round 1's INSERT + meta batch
		t.Fatalf("exec 왕복 누적 %d, want 2", execTrips())
	}
}

// TestShadowRoundFires — the fired round's full contract: every
// prediction-row field, the fired counter bump, the first-fire streak, the
// gate stamp, and exactly 5 round trips (gate 1 + verdict 1 + what-if 1 +
// INSERT 1 + meta batch 1).
func TestShadowRoundFires(t *testing.T) {
	registerFakeShadow(t)
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 7, 0, 5) // sample 12 >= 10, ratio 100% — fires

	fireRound(t, r)

	rows := shadowPredictions(t, r, "suggest_min_score")
	if len(rows) != 1 {
		t.Fatalf("예측 행 %d건, want 1", len(rows))
	}
	p := rows[0]
	if p.trigger != "auto_zero_near_high" {
		t.Fatalf("trigger = %q, want auto_zero_near_high", p.trigger)
	}
	if p.key != "suggest_min_score" {
		t.Fatalf("key = %q, want suggest_min_score", p.key)
	}
	if p.cur != "0.15" {
		t.Fatalf("cur = %q, want \"0.15\"(레지스트리 기본값 표기)", p.cur)
	}
	if p.newV != "0.05" {
		t.Fatalf("new = %q, want \"0.05\"(안전범위 하향 경계)", p.newV)
	}
	// What-if at the proposal: all 5 zero rows reclassify → 5 of 12 reads = 41%.
	if p.reclassN != 5 {
		t.Fatalf("pred_reclass_n = %d, want 5", p.reclassN)
	}
	if p.sampleN != 12 {
		t.Fatalf("sample_n = %d, want 12", p.sampleN)
	}
	if p.predPct != 41 {
		t.Fatalf("pred_zero_near_pct = %d, want 41", p.predPct)
	}
	for _, frag := range []string{"auto_zero_near_high", "suggest_min_score", "0.15", "0.05", "not causality"} {
		if !strings.Contains(p.rationale, frag) {
			t.Fatalf("rationale %q — 필수 문구 %q 누락", p.rationale, frag)
		}
	}
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "1" {
		t.Fatalf("fired 카운터 = %q, want \"1\"", got)
	}
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "1" {
		t.Fatalf("첫 발화 streak = %q, want \"1\"", got)
	}
	if got := rowTrips() + execTrips(); got != 5 {
		t.Fatalf("발화 라운드 왕복 %d, want 5(게이트 1+판정 1+재분류 1+INSERT 1+메타 1)", got)
	}
}

// TestShadowRoundNoFire — a sample-ok round below the warn ratio inserts
// nothing, keeps no streak or fired key, but bumps the round counter and
// stamps the gate (the round ran — r100-1) — 3 round trips (gate 1 +
// verdict 1 + the rounds+stamp batch 1).
func TestShadowRoundNoFire(t *testing.T) {
	registerFakeShadow(t)
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 11, 5, 1) // sample 17, ratio 16% < 30% — no fire

	fireRound(t, r)

	if p := shadowPredictions(t, r, "suggest_min_score"); len(p) != 0 {
		t.Fatalf("미발화 예측 행 %d건, want 0", len(p))
	}
	for _, key := range []string{"fired_auto_auto_zero_near_high", "auto_streak_suggest_min_score"} {
		if v := shadowMetaVal(t, r, key); v != "" {
			t.Fatalf("미발화 meta %s = %q, want 미기록", key, v)
		}
	}
	if _, err := time.Parse(time.RFC3339, shadowMetaVal(t, r, "last_shadow_at")); err != nil {
		t.Fatalf("미발화 게이트 스탬프 미기록 또는 비RFC3339: %q", shadowMetaVal(t, r, "last_shadow_at"))
	}
	if got := rowTrips() + execTrips(); got != 3 {
		t.Fatalf("미발화 라운드 왕복 %d, want 3(게이트 1+판정 1+스탬프 1)", got)
	}
}

// TestShadowStreakLifecycle — the streak rules through real rounds on one
// scratch DB: first fire 1 → value change with direction kept hits 2 → a
// non-fired round maintains → a direction reversal resets to 1 → a
// sample-floor miss resets to 0.
func TestShadowStreakLifecycle(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 7, 0, 5)

	// (a) First fire — streak 1.
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "1" {
		t.Fatalf("(a) 첫 발화 streak = %q, want \"1\"", got)
	}

	// (b) Value change, direction kept — hit (streak 2). The cur override
	// moves to 0.25; the proposal stays the down bound 0.05.
	setShadowMeta(t, r, "suggest_min_score", "0.25")
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "2" {
		t.Fatalf("(b) 값 변화 적중 streak = %q, want \"2\"", got)
	}
	rows := shadowPredictions(t, r, "suggest_min_score")
	if len(rows) != 2 || rows[1].cur != "0.25" || rows[1].newV != "0.05" {
		t.Fatalf("(b) 두 번째 예측 행 = %+v, want cur 0.25 → new 0.05", rows)
	}

	// (c) Non-fired round — maintain (streak stays 2, no new row).
	seedShadowReads(t, r, 40, 30, 2) // sample 92, ratio 2*100/32=6% < 30%
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "2" {
		t.Fatalf("(c) 미발화 유지 streak = %q, want \"2\"", got)
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 2 {
		t.Fatalf("(c) 미발화 후 예측 행 %d건, want 2(불변)", n)
	}

	// (d) Direction reversal — the cur override sits below the floor; the
	// range-restoring proposal 0.05 moves UP against the previous down —
	// reset to 1. Re-raise the ratio first (the (c) dilution must not
	// mask the fire).
	setShadowMeta(t, r, "suggest_min_score", "0.04")
	seedShadowReads(t, r, 0, 0, 30) // zero 67, reclass 37 → 55% ≥ 30%
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "1" {
		t.Fatalf("(d) 방향 반전 리셋 streak = %q, want \"1\"", got)
	}
	rows = shadowPredictions(t, r, "suggest_min_score")
	if len(rows) != 3 || rows[2].cur != "0.04" || rows[2].newV != "0.05" {
		t.Fatalf("(d) 반전 예측 행 = %+v, want cur 0.04 → new 0.05", rows)
	}

	// (e) Sample-floor miss — reset to 0 (and no row).
	if _, err := r.pool.Exec(context.Background(), `DELETE FROM read_events`); err != nil {
		t.Fatalf("read_events 정리: %v", err)
	}
	seedShadowReads(t, r, 2, 1, 0) // sample 3 < 10
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "0" {
		t.Fatalf("(e) 표본 미달 리셋 streak = %q, want \"0\"", got)
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 3 {
		t.Fatalf("(e) 표본 미달 후 예측 행 %d건, want 3(불변)", n)
	}
}

// TestShadowOffPreservesState — switching auto_mode off mid-accumulation
// keeps streak·fired·gate (persistence, not discard): off rounds change
// nothing, and the next shadow round continues the streak.
func TestShadowOffPreservesState(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 7, 0, 5)

	fireRound(t, r) // streak 1, fired 1
	setShadowMeta(t, r, "suggest_min_score", "0.25")
	fireRound(t, r) // streak 2, fired 2
	before := shadowMetaVal(t, r, "last_shadow_at")

	setShadowMeta(t, r, "auto_mode", "off")
	runShadowRound(t, r) // off — nothing changes (gate stamp untouched)
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "2" {
		t.Fatalf("off 경과 streak = %q, want \"2\"(잔존)", got)
	}
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "2" {
		t.Fatalf("off 경과 fired = %q, want \"2\"(잔존)", got)
	}
	if got := shadowMetaVal(t, r, "last_shadow_at"); got != before {
		t.Fatalf("off 경과 게이트 스탬프 %q, want %q(잔존)", got, before)
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 2 {
		t.Fatalf("off 경과 예측 행 %d건, want 2(불변)", n)
	}

	setShadowMeta(t, r, "auto_mode", "shadow")
	fireRound(t, r) // continues — streak 3
	if got := shadowMetaVal(t, r, "auto_streak_suggest_min_score"); got != "3" {
		t.Fatalf("재전환 streak = %q, want \"3\"(이어짐)", got)
	}
}

// TestShadowProposalStaysInRange — with the cur override dragged outside
// the safe range on both sides, every inserted proposal stays inside the
// closed [0.05, 0.30] range.
func TestShadowProposalStaysInRange(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 7, 0, 5)

	for _, cur := range []string{"0.40", "0.02"} {
		setShadowMeta(t, r, "suggest_min_score", cur)
		fireRound(t, r)
	}
	for i, p := range shadowPredictions(t, r, "suggest_min_score") {
		newF, err := strconv.ParseFloat(p.newV, 64)
		if err != nil {
			t.Fatalf("예측 %d행 new %q 파싱: %v", i, p.newV, err)
		}
		if newF < 0.05 || newF > 0.30 {
			t.Fatalf("예측 %d행 new %v — 안전범위 [0.05, 0.30] 밖 제안", i, newF)
		}
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 2 {
		t.Fatalf("예측 행 %d건, want 2", n)
	}
}

// TestShadowUnregisteredZeroCost — with no circuit registered (a
// composition root without the shadow wiring), the hook is a nil check —
// 0 round trips regardless of mode. The seam is forced to nil explicitly:
// the ops package's init registration (shadow_register.go) fires in any
// binary that transitively imports ops, so "unregistered" is a state
// this test must create, not assume.
func TestShadowUnregisteredZeroCost(t *testing.T) {
	savedRules, savedVerdict := shadowRulesFn, shadowVerdictFn
	RegisterShadowCircuit(nil, nil)
	t.Cleanup(func() { RegisterShadowCircuit(savedRules, savedVerdict) })
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	resetShadowGateCache(t)
	r.maybeShadowRound(context.Background())
	if got := rowTrips() + execTrips(); got != 0 {
		t.Fatalf("미등록 라운드 왕복 %d, want 0(nil 세임)", got)
	}
}

// TestShadowWhitelistEmptySkips — a registered circuit whose rules carry
// no shadow-grade key runs nothing (0 round trips — the whitelist gate
// precedes the gate read).
func TestShadowWhitelistEmptySkips(t *testing.T) {
	manual := fakeShadowRule()
	manual.Auto = "" // manual-only grade — outside the whitelist
	RegisterShadowCircuit(func() []ShadowRule { return []ShadowRule{manual} }, fakeShadowVerdict)
	t.Cleanup(func() { RegisterShadowCircuit(nil, nil) })
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	resetShadowGateCache(t)
	r.maybeShadowRound(context.Background())
	if got := rowTrips() + execTrips(); got != 0 {
		t.Fatalf("화이트리스트 밖 라운드 왕복 %d, want 0", got)
	}
}

// TestShadowSilentFailure — a broken substrate (the tuning_predictions
// table dropped mid-gate-read) never surfaces: no error, no panic.
func TestShadowSilentFailure(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	if _, err := r.pool.Exec(context.Background(), `DROP TABLE tuning_predictions`); err != nil {
		t.Fatalf("tuning_predictions 드롭: %v", err)
	}
	resetShadowGateCache(t)
	r.maybeShadowRound(context.Background()) // must return quietly
	if v := shadowMetaVal(t, r, "last_shadow_at"); v != "" {
		t.Fatalf("실패 라운드 게이트 스탬프 %q, want 미기록(재시도 열림)", v)
	}
}

// TestShadowRoundsCounterSplit — the r100-1 round counter: meta
// auto_rounds_<key> bumps on EVERY stamped round — fired, non-fired,
// and boundary — so "rounds ≥ 14" and "fires ≥ 5" stay independent
// gate inputs (the pre-fix tuning_predictions count only saw fired
// rounds with a proposal, starving the rounds condition).
func TestShadowRoundsCounterSplit(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 7, 0, 5) // sample 12, ratio 100% — fires

	// Round 1 — fired with a proposal: rounds 1, fired 1.
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "1" {
		t.Fatalf("(1) 발화 라운드 auto_rounds = %q, want \"1\"", got)
	}

	// Round 2 — non-fired (diluted below the warn ratio): rounds 2, fired
	// unchanged at 1 — the non-fired round still counts as a round.
	seedShadowReads(t, r, 40, 30, 2) // sample 92, ratio 6% < 30%
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "2" {
		t.Fatalf("(2) 미발화 라운드 auto_rounds = %q, want \"2\"", got)
	}
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "1" {
		t.Fatalf("(2) 미발화 라운드 fired = %q, want \"1\"(불변)", got)
	}

	// Round 3 — a boundary fire (cur at the down bound, no proposal):
	// rounds 3, fired 2 — the round and the fire each count once more.
	setShadowMeta(t, r, "suggest_min_score", "0.05")
	seedShadowReads(t, r, 0, 0, 30) // ratio 100% — fires again
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "3" {
		t.Fatalf("(3) 경계 발화 라운드 auto_rounds = %q, want \"3\"", got)
	}
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "2" {
		t.Fatalf("(3) 경계 발화 라운드 fired = %q, want \"2\"", got)
	}
}

// TestShadowBoundaryFireBumpsFired — the r100-3 boundary-fire contract:
// the trigger fired (ratio at or above the warn line) but cur sits
// exactly at the directional bound, so no strict move exists and no
// prediction row is appended (rows stay proposal-only per the spec) —
// yet the fired counter bumps (fired counts trigger-verdict fires) and
// the round stamps the gate and bumps the round counter.
func TestShadowBoundaryFireBumpsFired(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	setShadowMeta(t, r, "suggest_min_score", "0.05") // the down bound itself
	seedShadowReads(t, r, 7, 0, 5)                   // sample 12, ratio 100% — fires

	fireRound(t, r)

	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 0 {
		t.Fatalf("경계 발화 예측 행 %d건, want 0(제안 없음 — 행은 제안 있는 경우만)", n)
	}
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "1" {
		t.Fatalf("경계 발화 fired = %q, want \"1\"(트리거 판정 기준)", got)
	}
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "1" {
		t.Fatalf("경계 발화 auto_rounds = %q, want \"1\"", got)
	}
	if _, err := time.Parse(time.RFC3339, shadowMetaVal(t, r, "last_shadow_at")); err != nil {
		t.Fatalf("경계 발화 게이트 스탬프 미기록 또는 비RFC3339: %q", shadowMetaVal(t, r, "last_shadow_at"))
	}
}

// TestShadowNearScoreDomainGuard — the r101-2 domain guard: a zero row
// whose near_score sits outside the [0,1] score domain (a manual
// injection) never enters the reclassification pool — with the out-of-
// domain rows excluded the ratio stays below the warn line and the
// round does not fire (unguarded, 2 of 3 zero rows would read as
// reclass → 66% ≥ 30% and fire).
func TestShadowNearScoreDomainGuard(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	seedShadowReads(t, r, 9, 1, 0) // sample 10 (the floor), zero 1 in-domain
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO read_events (hits, zero, near, fallback, near_score) VALUES (0, true, false, false, 5.0)`); err != nil {
			t.Fatalf("시딩 도메인 밖 행: %v", err)
		}
	}

	fireRound(t, r)

	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 0 {
		t.Fatalf("도메인 밖 행 포함 시 예측 행 %d건, want 0(가드 제외 — 미발화)", n)
	}
	if v := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); v != "" {
		t.Fatalf("도메인 밖 행 발화 카운터 = %q, want 미기록", v)
	}
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "1" {
		t.Fatalf("미발화 라운드 auto_rounds = %q, want \"1\"", got)
	}
}

// TestShadowCounterCorruptReset — the r101-3 guarded increment: a
// non-numeric existing counter text (meta pollution) resets to '1'
// inside the CASE instead of failing the cast, the batch, and the gate
// stamp — a polluted counter must not permanently block the round.
func TestShadowCounterCorruptReset(t *testing.T) {
	registerFakeShadow(t)
	r := newTestRepo(t)
	setShadowMeta(t, r, "auto_mode", "shadow")
	setShadowMeta(t, r, "fired_auto_auto_zero_near_high", "x")   // non-numeric pollution
	setShadowMeta(t, r, "auto_rounds_suggest_min_score", "junk") // non-numeric pollution
	seedShadowReads(t, r, 7, 0, 5)                               // fires

	fireRound(t, r)

	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "1" {
		t.Fatalf("오염 fired 카운터 = %q, want \"1\"(리셋)", got)
	}
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "1" {
		t.Fatalf("오염 auto_rounds 카운터 = %q, want \"1\"(리셋)", got)
	}
	if _, err := time.Parse(time.RFC3339, shadowMetaVal(t, r, "last_shadow_at")); err != nil {
		t.Fatalf("오염 경과 게이트 스탬프 미기록: %q", shadowMetaVal(t, r, "last_shadow_at"))
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 1 {
		t.Fatalf("오염 경과 예측 행 %d건, want 1", n)
	}

	// r102-1 — the digit bound: an oversized numeric counter text (26
	// nines — far beyond int8) is the same pollution class as non-numeric
	// text. The old guard '^[0-9]+$' matched it, the ::bigint cast
	// overflowed, and the failed batch left the gate unstamped; the bound
	// '^[0-9]{1,18}$' routes it to the reset branch like any other
	// corrupt text.
	big := strings.Repeat("9", 26)
	setShadowMeta(t, r, "fired_auto_auto_zero_near_high", big)
	setShadowMeta(t, r, "auto_rounds_suggest_min_score", big)
	fireRound(t, r)
	if got := shadowMetaVal(t, r, "fired_auto_auto_zero_near_high"); got != "1" {
		t.Fatalf("26자리 fired 카운터 = %q, want \"1\"(자릿수 상한 리셋)", got)
	}
	if got := shadowMetaVal(t, r, "auto_rounds_suggest_min_score"); got != "1" {
		t.Fatalf("26자리 auto_rounds 카운터 = %q, want \"1\"(자릿수 상한 리셋)", got)
	}
	stamp, err := time.Parse(time.RFC3339, shadowMetaVal(t, r, "last_shadow_at"))
	if err != nil {
		t.Fatalf("26자리 경과 게이트 스탬프 미기록: %q", shadowMetaVal(t, r, "last_shadow_at"))
	}
	// the round restamped the gate (not the 25h-backdated reopen value a
	// failed batch would leave behind).
	if time.Since(stamp) > 10*time.Minute {
		t.Fatalf("26자리 경과 게이트 스탬프 미갱신(배치 실패 잔존): %s", stamp)
	}
}
