// maintain_test.go — small maintain contracts. (1) The --limit 500 call pattern the TUI
// loader uses for the 3 reports passes mode-ownership validation and the cap is reflected
// as-is in the store lookup (the ops-side half of abolishing invisible truncation — the
// TUI-side argument assembly uses ops.MaintainTUILoadLimit as the single source). (2) The
// usage line guides toward --tui entry. fakeMaintStore and runMaintainArgs reuse the
// care_test.go conventions.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
)

// limitCapturingStore — a fake capturing the DupGroups/ConflictGroups caps
// (fakeMaintStore does not record the limit — a thin wrapper needed only here).
type limitCapturingStore struct {
	*fakeMaintStore
	dupLimit      int
	conflictLimit int
}

func (f *limitCapturingStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	f.dupLimit = limit
	return nil, nil
}
func (f *limitCapturingStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	f.conflictLimit = limit
	return nil, nil
}

// runLimitArgs — a run helper that passes the wrapper store straight into RunMaintain
// (runMaintainArgs is fixed to *fakeMaintStore, so a local version here).
func runLimitArgs(t *testing.T, f *limitCapturingStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestMaintainTUILoadLimitContract — B3: the TUI suspects loader's call pattern
// (--limit 500 + mode) is not rejected as an invalid argument (exit 1/3) in any of the 3
// report modes, and the 500 cap flows as-is into the scan. The default caps
// (dup/conflict 10, stale 20) are for human interactivity — the TUI replaces them with an
// explicit cap, since truncation is not visible on its screen.
func TestMaintainTUILoadLimitContract(t *testing.T) {
	if MaintainTUILoadLimit != 500 {
		t.Fatalf("MaintainTUILoadLimit = %d, want 500(스펙 v1.6.1 B3)", MaintainTUILoadLimit)
	}
	f := &limitCapturingStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}

	// dup-suspects — exactly the argument pattern the TUI assembles.
	out, code := runLimitArgs(t, f, "--limit", "500", "--dup-suspects")
	if code != 0 || !strings.Contains(out, `"status":"dup-suspects"`) {
		t.Fatalf("dup-suspects exit=%d out=%s", code, out)
	}
	if f.dupLimit != 500 {
		t.Errorf("DupGroups limit = %d, want 500", f.dupLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--conflict-suspects")
	if code != 0 || !strings.Contains(out, `"status":"conflict-suspects"`) {
		t.Fatalf("conflict-suspects exit=%d out=%s", code, out)
	}
	if f.conflictLimit != 500 {
		t.Errorf("ConflictGroups limit = %d, want 500", f.conflictLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--stale")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("stale exit=%d out=%s", code, out)
	}
}

// TestMaintainUsageLineMentionsTUI — B4: the bare-usage guidance (no mode, exit 3)
// includes the --tui opt-in entry (agent self-correction — for cases where the error
// message's common suffix is the only discovery path).
func TestMaintainUsageLineMentionsTUI(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f)
	if code != 3 {
		t.Fatalf("bare exit = %d, want 3", code)
	}
	if !strings.Contains(out, "usage: flint maintain") {
		t.Fatalf("usage 부재: %s", out)
	}
	if !strings.Contains(out, "--tui") {
		t.Fatalf("usage에 --tui 부재: %s", out)
	}
}

// ── decay formulas and --decay-audit (the recall-reinforcement regression is in the knowledge package) ──

// TestEventEffectiveScoreBoundaries — the decay formula boundaries (v1.9.0 — consumed via
// the single definition knowledge.EventEffectiveScore. The old ops.EffectiveRecallScore
// wrapper was abolished with the derived-source switch). 1 day (linear -1), 100 days
// (floor 0), never recalled (count 0 = 0), and the rate override.
func TestEventEffectiveScoreBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		count int
		last  time.Time
		rate  float64
		want  int
	}{
		{"1일 경과 — count-1", 5, now.Add(-25 * time.Hour), 1, 4},
		{"100일 경과 — 하한 0", 5, now.Add(-100 * 24 * time.Hour), 1, 0},
		{"23시간 — 0일(절사) 감쇠 없음", 5, now.Add(-23 * time.Hour), 1, 5},
		{"미회상(count 0) — 0", 0, now.Add(-10 * 24 * time.Hour), 1, 0},
		{"rate 0.5 — 2일 -1", 5, now.Add(-2 * 24 * time.Hour), 0.5, 4},
		{"rate 2 — 가속 하한 0", 3, now.Add(-2 * 24 * time.Hour), 2, 0},
	}
	for _, c := range cases {
		if got := knowledge.EventEffectiveScore(c.count, c.last, now, c.rate); got != c.want {
			t.Errorf("%s: EventEffectiveScore(%d, %v, rate %v) = %d, want %d",
				c.name, c.count, c.last, c.rate, got, c.want)
		}
	}
}

// TestMaintainDecayAuditDerivedSource — the v1.9.0 derived-source regression: the query
// source must be the recall_events aggregate join (cnt, last_at) and must not reference
// the dropped v11 physical columns (knowledge.recall_score, last_recalled_at).
func TestMaintainDecayAuditDerivedSource(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	var gotSQL string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			gotSQL = sql
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if !strings.Contains(gotSQL, "recall_events") {
		t.Fatalf("조회 원천에 recall_events 파생 조인 부재: %s", gotSQL)
	}
	for _, gone := range []string{"k.recall_score", "k.last_recalled_at", ", recall_score", ", last_recalled_at"} {
		if strings.Contains(gotSQL, gone) {
			t.Fatalf("폐기된 v11 실열 %q 참조 잔존: %s", gone, gotSQL)
		}
	}
}

// decayAuditRowsFixture — the --decay-audit query source rows (synthetic demo — after the
// v1.9.0 derived switch and the v1.13.0 T2 tags tail, the 4 columns id, cnt (event count),
// last_at (last recall time), and tags). Also verifies that never-recalled (NULL —
// reproducing the Go defense for the server-side WHERE exclusion) and 0-day-elapsed rows
// are excluded by the candidate filter, and that a :vol:immutable: row keeps its count
// (exempt — not a decay candidate).
func decayAuditRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(3), int32(5), now.Add(-100 * 24 * time.Hour), ":adr:"},           // fully decayed (floor 0)
		{int32(1), int32(5), now.Add(-25 * time.Hour), ":adr:"},                 // 1-day decay
		{int32(2), int32(7), nil, ":adr:"},                                      // never recalled — excluded
		{int32(4), int32(9), now.Add(-1 * time.Hour), ":adr:"},                  // 0 days — excluded
		{int32(6), int32(8), now.Add(-100 * 24 * time.Hour), ":vol:immutable:"}, // exempt — eff==cnt, excluded
	}
}

// decayAuditJSON — for parsing the --decay-audit output.
type decayAuditJSON struct {
	Status       string  `json:"status"`
	DecayRate    float64 `json:"decay_rate"`
	ArchiveBelow float64 `json:"archive_below"`
	Count        int     `json:"count"`
	Items        []struct {
		ID           int    `json:"id"`
		Score        int    `json:"score"`
		LastRecalled string `json:"last_recalled"`
		Effective    int    `json:"effective"`
		Recommend    string `json:"recommend"`
	} `json:"items"`
}

// TestMaintainDecayAuditList — the decay-candidate listing — lists only
// decay-progressed rows (never-recalled and 0-day excluded); the default decay_rate=1
// and archive_below=0 (inactive — all recommends empty).
func TestMaintainDecayAuditList(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil // meta (thresholds) empty — defaults
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "decay-audit" || got.Count != 2 || len(got.Items) != 2 {
		t.Fatalf("status/count/items = %q/%d/%d, want decay-audit/2/2 — %s", got.Status, got.Count, len(got.Items), out)
	}
	if got.DecayRate != 1 || got.ArchiveBelow != 0 {
		t.Errorf("기본 임계값 decay_rate=%v archive_below=%v, want 1/0", got.DecayRate, got.ArchiveBelow)
	}
	first, second := got.Items[0], got.Items[1]
	if first.ID != 3 || first.Score != 5 || first.Effective != 0 || first.Recommend != "" {
		t.Errorf("items[0] = %+v, want id3 score5 eff0(100일 하한) recommend 없음", first)
	}
	if second.ID != 1 || second.Score != 5 || second.Effective != 4 {
		t.Errorf("items[1] = %+v, want id1 score5 eff4(1일)", second)
	}
	if want := now.Add(-25 * time.Hour).Format(time.RFC3339); second.LastRecalled != want {
		t.Errorf("items[1].last_recalled = %q, want %q", second.LastRecalled, want)
	}
}

// TestMaintainDecayAuditLimit — the --limit cap is reflected in the listing (the stale convention).
func TestMaintainDecayAuditLimit(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return decayAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("limit 1: count/items = %d/%d, want 1/1 — %s", got.Count, len(got.Items), out)
	}
}

// TestMaintainDecayAuditThresholdOverride — the meta-override decay_rate and
// archive_below are reflected in the formula and recommendation (effective < archive_below -> archive).
// → recommend "archive").
func TestMaintainDecayAuditThresholdOverride(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return [][]any{
				{int32(1), int32(5), now.Add(-6 * 24 * time.Hour), ":adr:"},  // 5-0.5*6=2 → archive
				{int32(2), int32(10), now.Add(-4 * 24 * time.Hour), ":adr:"}, // 10-0.5*4=8 -> kept
			}, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"decay_rate", "0.5"}, {"archive_below", "3"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.DecayRate != 0.5 || got.ArchiveBelow != 3 {
		t.Fatalf("오버라이드 decay_rate=%v archive_below=%v, want 0.5/3 — %s", got.DecayRate, got.ArchiveBelow, out)
	}
	if got.Items[0].ID != 1 || got.Items[0].Effective != 2 || got.Items[0].Recommend != "archive" {
		t.Errorf("items[0] = %+v, want id1 eff2 recommend archive", got.Items[0])
	}
	if got.Items[1].ID != 2 || got.Items[1].Effective != 8 || got.Items[1].Recommend != "" {
		t.Errorf("items[1] = %+v, want id2 eff8 recommend 없음", got.Items[1])
	}
}

// TestMaintainDecayAuditRejectsForeignFlags — mode-ownership validation: --decay-audit
// is a read-only report — unrelated value flags like --days are rejected with an error (exit 1).
func TestMaintainDecayAuditRejectsForeignFlags(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--decay-audit", "--days", "5")
	if code != 1 {
		t.Fatalf("exit = %d, want 1(무관 플래그 거부): %s", code, out)
	}
}

// ── v1.13.0 T3: the stale score (2 weighted signals — the --age-audit scoring source) ──

// TestStaleScoreFormula — the pure weighted-sum formula: 0-100 normalized,
// monotone in each signal, saturated at ttl 180d·corr 3 (weights 0.7/0.3 —
// code constants, part of the formula, not tuning values).
func TestStaleScoreFormula(t *testing.T) {
	cases := []struct {
		name string
		ttl  int
		corr int
		want int
	}{
		{"계약: 0·0 → 0", 0, 0, 0},
		{"계약: TTL 포화 180·corr 0 → 70", 180, 0, 70},
		{"계약: corr 포화 0·3 → 30", 0, 3, 30},
		{"계약: 이중 포화 180·3 → 100", 180, 3, 100},
		{"중간: 90·0 → 35", 90, 0, 35},
		{"중간: 0·2 → 20", 0, 2, 20},
		{"중간: 90·2 → 55", 90, 2, 55},
		{"포화 초과 상한: 400·9 → 100", 400, 9, 100},
	}
	for _, c := range cases {
		if got := StaleScore(c.ttl, c.corr); got != c.want {
			t.Errorf("%s: StaleScore(%d, %d) = %d, want %d", c.name, c.ttl, c.corr, got, c.want)
		}
	}
	// Monotonicity in each signal (the other held fixed) — the scoring
	// contract: more elapsed TTL·more corrections never lower the score.
	for ttl := 0; ttl <= 360; ttl += 20 {
		if StaleScore(ttl+20, 1) < StaleScore(ttl, 1) {
			t.Fatalf("TTL 단조성 위반: %d→%d", ttl, ttl+20)
		}
	}
	for corr := 0; corr < 6; corr++ {
		if StaleScore(100, corr+1) < StaleScore(100, corr) {
			t.Fatalf("corr 단조성 위반: %d→%d", corr, corr+1)
		}
	}
	// Independence: the correction signal moves the score at every TTL level.
	for ttl := 0; ttl <= 180; ttl += 45 {
		if StaleScore(ttl, 3) <= StaleScore(ttl, 0) {
			t.Fatalf("신호 독립 위반: ttl %d에서 corr 0→3 무효", ttl)
		}
	}
}

// staleScoredRowsFixture — the listStaleScored query shape (synthetic): id,
// topic, ttl reference (COALESCE(re.last_at, ingested_at)), same-topic
// correction count, bad-recall count, supersede count.
func staleScoredRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(11), "t3-오래미회상", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0)},
		{int32(12), "t3-교정다발", now.Add(-10 * 24 * time.Hour), int32(3), int32(0), int32(0)},
		{int32(13), "t3-참조열", now.Add(-100 * 24 * time.Hour), int32(0), int32(2), int32(1)},
	}
}

// TestListStaleScoredFake — the listing contract on a fake: the SQL excludes
// :vol:immutable: server-side (strpos, NULL-tags safe), counts same-topic
// :correction: rows via strpos, joins the recall_events aggregate, and caps
// the population with LIMIT $1; the items carry the 2 signal values + the 2
// non-weighted reference columns, and the output order is score DESC.
func TestListStaleScoredFake(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	var gotSQL string
	var gotArgs []any
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			gotSQL, gotArgs = sql, args
			return staleScoredRowsFixture(now), nil
		}
		return nil, nil
	}}}
	items, err := listStaleScored(context.Background(), f, 0)
	if err != nil {
		t.Fatalf("listStaleScored: %v", err)
	}
	for _, frag := range []string{
		"strpos(k.tags, ':vol:immutable:') = 0",
		"k.tags IS NULL OR strpos(k.tags, ':vol:immutable:') = 0",
		"strpos(c.tags, ':correction:') > 0",
		"recall_events",
		"LIMIT $1",
	} {
		if !strings.Contains(gotSQL, frag) {
			t.Errorf("조회에 %q 부재: %s", frag, gotSQL)
		}
	}
	if len(gotArgs) != 1 || gotArgs[0].(int) != 20 {
		t.Errorf("LIMIT 인자 = %v, want [20](기본 캡 — decay-audit 선례)", gotArgs)
	}
	if len(items) != 3 {
		t.Fatalf("항목 수 = %d, want 3: %+v", len(items), items)
	}
	// Scores: 오래미회상 ttl200·corr0 → 70; 교정다발 ttl10·corr3 → 34;
	// 참조열 ttl100·corr0 → 39 (reference columns carry no weight — 39 not 100+).
	byID := map[int]staleScoredItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if s := byID[11]; s.Score != 70 || s.TTLDays != 200 || s.CorrCount != 0 {
		t.Errorf("id11 = %+v, want score70 ttl200 corr0", s)
	}
	if s := byID[12]; s.Score != 34 || s.TTLDays != 10 || s.CorrCount != 3 {
		t.Errorf("id12 = %+v, want score34 ttl10 corr3", s)
	}
	if s := byID[13]; s.Score != 39 || s.BadRecall != 2 || s.Supersede != 1 {
		t.Errorf("id13 = %+v, want score39(참조열 비가중) bad2 sup1", s)
	}
	// Output order: score DESC (70 > 39 > 34).
	if items[0].ID != 11 || items[1].ID != 13 || items[2].ID != 12 {
		t.Errorf("순서 = %d,%d,%d, want 11,13,12(점수 내림)", items[0].ID, items[1].ID, items[2].ID)
	}
}

// staleSeedRec — a synthetic knowledge record for the slow stale-score seed
// (the knowledge-package hotRec convention — no personal data).
func staleSeedRec(topic, hash, status, tags string) map[string]any {
	return map[string]any{
		"kind":         "knowledge",
		"topic":        topic,
		"summary":      "요약-" + hash,
		"tags":         tags,
		"confidence":   0.9,
		"content_hash": hash,
		"status":       status,
	}
}

// TestStaleScoreRealSlow — slow (FLINT_TEST_DSN — the verify-gate DB):
// synthetic seed on an isolated scratch DB, then the measured contracts:
// TTL monotonicity (older reference → higher score), the correction signal
// moving a fresh row above an uncorrected fresh row, the server-side
// :vol:immutable: exclusion, and the 2 non-weighted reference columns.
func TestStaleScoreRealSlow(t *testing.T) {
	if os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	dsn := os.Getenv("FLINT_TEST_DSN")
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer base.Close()
	name := fmt.Sprintf("flint_t3stale_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	repo, err := db.NewRepo(ctx, u.String())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// reportSlowStore (the package's shared real-DB wire.Store — repo+pool).
	// UPDATE seeding goes through Query (pool.Query executes any statement).
	st := &reportSlowStore{repo: repo, pool: pool}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const prefix = "t3-stale-"
	// The correction·reference rows SHARE the candidate's topic (the
	// signals are same-topic counts): the corr topic carries 3 correction-
	// tagged rows, the ref topic a superseded-after-recall row.
	ids, err := st.InsertBatch(ctx, []map[string]any{
		staleSeedRec(prefix+"old", "t3-old", "active", ":adr:"),
		staleSeedRec(prefix+"corr", "t3-corr", "active", ":adr:"),
		staleSeedRec(prefix+"corr", "t3-cx1", "superseded", ":correction:"),
		staleSeedRec(prefix+"corr", "t3-cx2", "superseded", ":correction:"),
		staleSeedRec(prefix+"corr", "t3-cx3", "superseded", ":correction:"),
		staleSeedRec(prefix+"ref", "t3-ref", "active", ":adr:"),
		staleSeedRec(prefix+"ref", "t3-rd", "superseded", ":adr:"),
		staleSeedRec(prefix+"imm", "t3-imm", "active", ":adr::vol:immutable:"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	// TTL aging: the old row 200d, the corr·ref rows 10d, immutable 300d
	// (ingested_at is set directly — the listStale convention's fallback).
	for id, d := range map[int]int{ids[0]: 200, ids[1]: 10, ids[5]: 10, ids[7]: 300} {
		if _, err := st.Query(ctx, `UPDATE knowledge SET ingested_at = now() - ($1 || ' days')::interval WHERE id = $2`, fmt.Sprint(d), id); err != nil {
			t.Fatalf("시드 경과일 적용 id=%d: %v", id, err)
		}
	}
	// The ref row's reference history: a same-topic superseded row that had
	// been recalled before supersession (bad_recall = invalidated/superseded
	// after recall).
	if _, err := st.Query(ctx,
		`UPDATE knowledge SET superseded_by = $1 WHERE id = $2`, ids[5], ids[6]); err != nil {
		t.Fatalf("supersede 시드: %v", err)
	}
	if _, err := st.Query(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT $1::int, now() - interval '5 days'`, ids[6]); err != nil {
		t.Fatalf("bad_recall 이벤트 시드: %v", err)
	}

	items, err := listStaleScored(ctx, st, 50)
	if err != nil {
		t.Fatalf("listStaleScored: %v", err)
	}
	byTopic := map[string]staleScoredItem{}
	for _, it := range items {
		byTopic[strings.TrimPrefix(it.Topic, prefix)] = it
	}
	old, hasOld := byTopic["old"]
	corr, hasCorr := byTopic["corr"]
	imm, hasImm := byTopic["imm"]
	ref, hasRef := byTopic["ref"]
	if !hasOld || !hasCorr || !hasRef {
		t.Fatalf("후보 누락: old=%v corr=%v ref=%v — %+v", hasOld, hasCorr, hasRef, items)
	}
	if hasImm {
		t.Fatalf("immutable 행이 후보에 존재: %+v(서버측 strpos 제외 위반)", imm)
	}
	// TTL monotonicity measured: old (200d·corr 0 → 70) above corr (10d·3 → 34).
	if old.Score <= corr.Score {
		t.Errorf("TTL 단조성: old %d ≤ corr %d — %+v / %+v", old.Score, corr.Score, old, corr)
	}
	// Correction independence measured: the fresh corrected row (34) is
	// scored·listed (a fresh uncorrected row would be ~4).
	if corr.CorrCount != 3 || corr.Score < 30 || corr.Score > 38 {
		t.Errorf("corr 신호 = %+v, want corr3·점수 34±4", corr)
	}
	// Reference columns (non-weighted): the ref row carries bad_recall 1·
	// supersede 1 without any score inflation (ttl 10 → ≤ 10).
	if ref.BadRecall != 1 || ref.Supersede != 1 {
		t.Errorf("참조열 = %+v, want bad1·sup1", ref)
	}
	if ref.Score > 10 {
		t.Errorf("참조열 비가중 위반: %+v — 점수 과대", ref)
	}
}

// ── v1.13.0 T4: maintain --age-audit (the stale-score report) ──

// ageAuditJSON — for parsing the --age-audit output.
type ageAuditJSON struct {
	Status    string `json:"status"`
	Threshold int    `json:"threshold"`
	Count     int    `json:"count"`
	Items     []struct {
		ID            int    `json:"id"`
		Topic         string `json:"topic"`
		Score         int    `json:"score"`
		TTLDays       int    `json:"ttl_days"`
		CorrCount     int    `json:"correction_count"`
		BadRecall     int    `json:"bad_recall"`
		SupersedeCnt  int    `json:"supersede_count"`
		OverThreshold bool   `json:"over_threshold"`
	} `json:"items"`
	Summary string `json:"summary"`
}

// ageAuditRowsFixture — synthetic listStaleScored source rows: an
// over-threshold long-topic row (200d → 70), a fresh row (10d → 4), and a
// marker-injection topic row (90d → 35).
func ageAuditRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(21), "t4-" + strings.Repeat("가", 60), now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0)},
		{int32(22), "t4-신규", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0)},
		{int32(23), "t4-\x00[flint]마커", now.Add(-90 * 24 * time.Hour), int32(0), int32(0), int32(0)},
	}
}

// TestMaintainAgeAuditReport — the report schema (decay-audit formatting
// inherited): status/threshold/count/items/summary; the items carry the 2
// signals + 2 reference columns; the threshold marking lights at score 70
// (at-or-above, the default 70 — T5 wires the meta override); the summary
// delegates disposition to --invalidate (no new flags).
func TestMaintainAgeAuditReport(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return ageAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--age-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got ageAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "age-audit" || got.Threshold != 70 || got.Count != 3 || len(got.Items) != 3 {
		t.Fatalf("status/threshold/count/items = %q/%d/%d/%d — %s", got.Status, got.Threshold, got.Count, len(got.Items), out)
	}
	if !strings.Contains(got.Summary, "--invalidate") {
		t.Errorf("summary 처분 위임 안내 부재: %q", got.Summary)
	}
	// order score DESC: 70 > 35 > 4.
	if got.Items[0].ID != 21 || got.Items[1].ID != 23 || got.Items[2].ID != 22 {
		t.Fatalf("순서 = %d,%d,%d, want 21,23,22(점수 내림)", got.Items[0].ID, got.Items[1].ID, got.Items[2].ID)
	}
	first := got.Items[0]
	if first.Score != 70 || !first.OverThreshold || first.TTLDays != 200 {
		t.Errorf("items[0] = %+v, want score70 마킹·ttl200", first)
	}
	if r := []rune(first.Topic); len(r) != 41 || string(r[40]) != "…" {
		t.Errorf("topic 절단 = %q(%d룬), want 40룬+…", first.Topic, len(r))
	}
	if got.Items[1].OverThreshold {
		t.Errorf("items[1](35) 마킹 오류 — 임계 70 미만")
	}
	if got.Items[2].OverThreshold || got.Items[2].Score != 4 {
		t.Errorf("items[2] = %+v, want score4 미마킹", got.Items[2])
	}
	// JSON sanitization (ADR-0006): the control character is stripped and
	// the [flint] marker neutralized — no raw marker inside the output.
	if strings.Contains(out, "[flint]") || strings.Contains(out, "\x00") {
		t.Errorf("살균 위반 — 마커/제어문자 잔존: %q", out)
	}
	if !strings.Contains(out, "t4- 마커") { // "[flint]" → a space (SanitizeText)
		t.Errorf("살균 후 토픽 잔존 위반: %q", out)
	}
}

// TestMaintainAgeAuditDryRunNoWrites — the report is read-only: no
// Invalidate/Commit calls (disposition is delegated to --invalidate).
func TestMaintainAgeAuditDryRunNoWrites(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return ageAuditRowsFixture(now), nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--age-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.invalidatedIDs) != 0 || len(f.commits) != 0 {
		t.Errorf("dry-run 위반: invalidate=%v commit=%v", f.invalidatedIDs, f.commits)
	}
}

// TestMaintainAgeAuditParserNoPrefixCollision — the --stale family is
// exact-match: --age-audit elects its own mode (never --stale's ok report),
// a bare --age prefix is an unknown flag (exit 3), --age-audit rejects the
// foreign --days flag (mode ownership), and the existing --stale /
// --invalidate-stale modes keep working after the addition.
func TestMaintainAgeAuditParserNoPrefixCollision(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	newStore := func() *fakeMaintStore {
		return &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM knowledge") {
				return ageAuditRowsFixture(now), nil
			}
			return nil, nil
		}}}
	}
	// --age-audit runs its own mode (the age-audit JSON, not the stale ok).
	out, code := runMaintainArgs(t, newStore(), "--age-audit")
	if code != 0 || !strings.Contains(out, `"status":"age-audit"`) || strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("--age-audit exit=%d out=%s, want age-audit 리포트", code, out)
	}
	// a bare prefix is an unknown flag (ParseArgs exact matching).
	out, code = runMaintainArgs(t, newStore(), "--age")
	if code != 3 {
		t.Fatalf("--age exit = %d, want 3(정확 일치 — 미등록 플래그): %s", code, out)
	}
	// --age-audit rejects the foreign --days (mode ownership, the decay-audit convention).
	out, code = runMaintainArgs(t, newStore(), "--age-audit", "--days", "5")
	if code != 1 {
		t.Fatalf("--age-audit --days exit = %d, want 1(무관 플래그 거부): %s", code, out)
	}
	// the existing --stale family is unchanged (no ghost-mode regression).
	out, code = runMaintainArgs(t, newStore(), "--stale")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("--stale exit=%d out=%s, want 기존 ok 리포트", code, out)
	}
	out, code = runMaintainArgs(t, newStore(), "--invalidate-stale")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("--invalidate-stale exit=%d out=%s, want dry-run ok", code, out)
	}
}
