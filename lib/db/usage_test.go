// usage_test.go — playbook use logs·success rate (real-DB slow tests).
package db

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// TestPruneDailyKeyPatternCoversGapQueryKeys — the gapq counter key
// (daily:gapq:<sha256>:<date>, v1.12.0 T2) falls inside the PruneDaily
// retention-window regex (auto-expiry with the date rollover), while the
// legacy shapes stay matched and dateless·non-daily keys stay out.
func TestPruneDailyKeyPatternCoversGapQueryKeys(t *testing.T) {
	re := regexp.MustCompile(pruneDailyKeyPattern)
	hash := strings.Repeat("a1", 32)
	for _, k := range []string{
		"daily:gapq:" + hash + ":2026-11-21",
		"daily:write:2026-11-21",
		"daily:read_bytes:2026-11-21",
		"daily:2026-11-21",
	} {
		if !re.MatchString(k) {
			t.Errorf("%s 미매치 — PruneDaily 만료 누락", k)
		}
	}
	for _, k := range []string{
		"daily:gapq:" + hash,
		"daily:gapq:" + hash + ":not-a-date",
		"last_write_at",
	} {
		if re.MatchString(k) {
			t.Errorf("%s 매치 — 의도 외 만료", k)
		}
	}
}

// cleanPBKeys — wipes the pb_% meta keys to isolate tests. LIKE's '_'
// is any character, so it is escaped with a backslash to be literal.
func cleanPBKeys(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key LIKE 'pb\_%'`); err != nil {
		t.Fatalf("clean pb keys: %v", err)
	}
}

// TestPlaybookStats — 2 successes out of 3 → Uses 3·OK 2·Rate 0.667.
func slowPlaybookStats(t *testing.T, r *Repo) {
	ctx := context.Background()
	cleanPBKeys(t, r)
	for _, ok := range []bool{true, false, true} {
		if err := r.LogPlaybookUse(ctx, 42, ok); err != nil {
			t.Fatalf("LogPlaybookUse(ok=%v): %v", ok, err)
		}
	}
	// Direct meta-key verification — increments exactly 3/2
	// (non-idempotence is the intentional log contract).
	var uses, oks string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'pb_42_uses'`).Scan(&uses); err != nil {
		t.Fatalf("read pb_42_uses: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'pb_42_ok'`).Scan(&oks); err != nil {
		t.Fatalf("read pb_42_ok: %v", err)
	}
	if uses != "3" || oks != "2" {
		t.Fatalf("meta uses=%q ok=%q, want 3/2", uses, oks)
	}
	stats, err := r.PlaybookStats(ctx)
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats len=%d, want 1 (%v)", len(stats), stats)
	}
	s := stats[0]
	if s.ID != 42 || s.Uses != 3 || s.OK != 2 {
		t.Fatalf("stat = {ID:%d Uses:%d OK:%d}, want {42 3 2}", s.ID, s.Uses, s.OK)
	}
	if s.Rate != 0.667 {
		t.Fatalf("rate = %v, want 0.667(셋째 자리 반올림)", s.Rate)
	}
}

// TestPlaybookStatsMultiple — 2 playbooks, id-ascending; only successes
// increment ok.
func slowPlaybookStatsMultiple(t *testing.T, r *Repo) {
	ctx := context.Background()
	cleanPBKeys(t, r)
	if err := r.LogPlaybookUse(ctx, 7, true); err != nil {
		t.Fatalf("LogPlaybookUse: %v", err)
	}
	if err := r.LogPlaybookUse(ctx, 7, true); err != nil {
		t.Fatalf("LogPlaybookUse: %v", err)
	}
	if err := r.LogPlaybookUse(ctx, 3, false); err != nil {
		t.Fatalf("LogPlaybookUse: %v", err)
	}
	stats, err := r.PlaybookStats(ctx)
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats len=%d, want 2 (%v)", len(stats), stats)
	}
	// The id-ascending sort contract.
	if stats[0].ID != 3 || stats[0].Uses != 1 || stats[0].OK != 0 || stats[0].Rate != 0 {
		t.Fatalf("stats[0] = %+v, want {3 1 0 0}", stats[0])
	}
	if stats[1].ID != 7 || stats[1].Uses != 2 || stats[1].OK != 2 || stats[1].Rate != 1 {
		t.Fatalf("stats[1] = %+v, want {7 2 2 1}", stats[1])
	}
}

// TestPlaybookStatsEmpty — no logs yields an empty slice·nil error.
func slowPlaybookStatsEmpty(t *testing.T, r *Repo) {
	cleanPBKeys(t, r)
	stats, err := r.PlaybookStats(context.Background())
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats = %v, want empty", stats)
	}
}

// TestShadowHookOnReadPath — the v1.23.0 4th read-tail hook through the
// REAL RecordReadEvent path (not the direct maybeShadowRound call): with
// the circuit registered and fire-shaped data seeded, the unconfigured
// off default suppresses the round (the single batched gate+mode read,
// no writes), and the warm steady state adds 0 shadow round trips.
func TestShadowHookOnReadPath(t *testing.T) {
	registerFakeShadow(t)
	rowTrips, execTrips := countShadowTrips(t)
	r := newTestRepo(t)
	seedShadowReads(t, r, 11, 1, 5) // fire-shaped — off must suppress it

	// Cold — the hook runs the batched gate+mode read exactly once.
	resetShadowGateCache(t)
	if err := r.RecordReadEvent(context.Background(), ReadEvent{Hits: 1}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	if got := rowTrips() + execTrips(); got != 1 {
		t.Fatalf("off 읽기 경로 shadow 왕복 %d, want 1(게이트+모드 배치)", got)
	}
	if n := len(shadowPredictions(t, r, "suggest_min_score")); n != 0 {
		t.Fatalf("off 읽기 경로 예측 행 %d건, want 0", n)
	}

	// Warm — 0 shadow round trips on the hot read path.
	warmShadowGateCache()
	if err := r.RecordReadEvent(context.Background(), ReadEvent{Hits: 1}); err != nil {
		t.Fatalf("RecordReadEvent(2): %v", err)
	}
	if got := rowTrips() + execTrips(); got != 1 {
		t.Fatalf("steady-state 읽기 경로 shadow 왕복 누적 %d, want 1(추가 0)", got)
	}
}

// ── v1.24.0 T2 — the knowledge.kind column stamp (StampKnowledgeKind) ──

// stampQuerier — a Querier fake recording the stamp call (SQL·args).
type stampQuerier struct {
	calls []stampCall
}

type stampCall struct {
	SQL  string
	Args []any
}

func (q *stampQuerier) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	q.calls = append(q.calls, stampCall{SQL: sql, Args: args})
	return nil, nil
}

// TestStampKnowledgeKindContract — the classification stamp: one batched
// UPDATE over the id list with the vocabulary value as $1; an empty id
// list·an empty kind are no-ops (no query at all — the unspecified-NULL
// contract never reaches the DB).
func TestStampKnowledgeKindContract(t *testing.T) {
	q := &stampQuerier{}
	if err := StampKnowledgeKind(context.Background(), q, []int{1, 2}, "preference"); err != nil {
		t.Fatalf("stamp err: %v", err)
	}
	if len(q.calls) != 1 {
		t.Fatalf("stamp 질의=%d회, want 1", len(q.calls))
	}
	if !strings.Contains(q.calls[0].SQL, "UPDATE knowledge SET kind = $1 WHERE id = ANY($2)") {
		t.Fatalf("stamp SQL 형태 불일치: %q", q.calls[0].SQL)
	}
	if q.calls[0].Args[0] != "preference" {
		t.Fatalf("스탬프 값=%v, want preference", q.calls[0].Args[0])
	}
	// no-ops: empty ids·empty kind issue no query.
	for _, tc := range []struct {
		ids  []int
		kind string
	}{
		{nil, "fact"},
		{[]int{1}, ""},
	} {
		qq := &stampQuerier{}
		if err := StampKnowledgeKind(context.Background(), qq, tc.ids, tc.kind); err != nil {
			t.Fatalf("no-op err: %v", err)
		}
		if len(qq.calls) != 0 {
			t.Fatalf("no-op(%v,%q)이 질의 발생: %+v", tc.ids, tc.kind, qq.calls)
		}
	}
}

// TestMeteringRegistryCurationV1240 — the D2 funnel pair is registered in
// the curation group (the write path's BumpCurationCounter whitelist
// derives from this registry — an unregistered kind would be silently
// unmetered).
func TestMeteringRegistryCurationV1240(t *testing.T) {
	kinds := MeteringKinds("curation")
	seen := map[string]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	for _, want := range []string{"correction_preference", "correction_fact"} {
		if !seen[want] {
			t.Errorf("curation 그룹에 %s 미등록 — D2 적립 불가", want)
		}
	}
}
