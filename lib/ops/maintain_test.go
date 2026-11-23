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
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
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
		if got := knowledge.EventEffectiveScore(c.count, c.last, now, c.rate, 0, 0); got != c.want {
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
		if got := StaleScore(c.ttl, c.corr, 180); got != c.want {
			t.Errorf("%s: StaleScore(%d, %d) = %d, want %d", c.name, c.ttl, c.corr, got, c.want)
		}
	}
	// Monotonicity in each signal (the other held fixed) — the scoring
	// contract: more elapsed TTL·more corrections never lower the score.
	for ttl := 0; ttl <= 360; ttl += 20 {
		if StaleScore(ttl+20, 1, 180) < StaleScore(ttl, 1, 180) {
			t.Fatalf("TTL 단조성 위반: %d→%d", ttl, ttl+20)
		}
	}
	for corr := 0; corr < 6; corr++ {
		if StaleScore(100, corr+1, 180) < StaleScore(100, corr, 180) {
			t.Fatalf("corr 단조성 위반: %d→%d", corr, corr+1)
		}
	}
	// Independence: the correction signal moves the score at every TTL level.
	for ttl := 0; ttl <= 180; ttl += 45 {
		if StaleScore(ttl, 3, 180) <= StaleScore(ttl, 0, 180) {
			t.Fatalf("신호 독립 위반: ttl %d에서 corr 0→3 무효", ttl)
		}
	}
	// The TTL saturation is the stale_ttl_days threshold (T5): moving it to
	// 90 re-lights the TTL component earlier (90d → 70, not 35)·the
	// non-positive defense falls back to the default 180.
	if got := StaleScore(90, 0, 90); got != 70 {
		t.Errorf("TTL 포화 이동(90일 임계): StaleScore(90,0,90) = %d, want 70", got)
	}
	if got := StaleScore(90, 0, 0); got != 35 {
		t.Errorf("비양수 포화 방어: StaleScore(90,0,0) = %d, want 35(기본 180 폴백)", got)
	}
}

// staleScoredRowsFixture — the listStaleScored query shape (synthetic): id,
// topic, ttl reference (COALESCE(re.last_at, ingested_at)), same-topic
// correction count, bad-recall count, supersede count, tags cell (the v1.22.0
// T4 suggestion input column).
func staleScoredRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(11), "t3-오래미회상", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":adr:"},
		{int32(12), "t3-교정다발", now.Add(-10 * 24 * time.Hour), int32(3), int32(0), int32(0), ":env:"},
		{int32(13), "t3-참조열", now.Add(-100 * 24 * time.Hour), int32(0), int32(2), int32(1), ":vol:conditional:"},
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
	// Scores: the 200d row ttl200·corr0 → 70; the corrected row ttl10·corr3 →
	// 34; the reference row ttl100·corr0 → 39 (reference columns carry no
	// weight — 39, not 100+).
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
		SuggestedVol  string `json:"suggested_vol"`
		VolTag        string `json:"vol_tag"`
	} `json:"items"`
	Summary string `json:"summary"`
}

// ageAuditRowsFixture — synthetic listStaleScored source rows: an
// over-threshold long-topic row (200d → 70), a fresh row (10d → 4), and a
// marker-injection topic row (90d → 35).
func ageAuditRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(21), "t4-" + strings.Repeat("가", 60), now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":adr:"},
		{int32(22), "t4-신규", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:"},
		{int32(23), "t4-\x00[flint]마커", now.Add(-90 * 24 * time.Hour), int32(0), int32(0), int32(0), ":vol:conditional:"},
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

// ── v1.22.0 T4: the vol-class suggestion columns on the --age-audit report ──

// ageAuditVolRowsFixture — synthetic rows for the suggestion columns: an
// immutable-class row (ADR topic, 200d → 70), a volatile-class row (version
// topic, 10d → 4), an already-approved :vol:conditional: row (90d → 35), and
// a plain unmatched row (5d → 2). The tags cell is the T4 input column.
func ageAuditVolRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(31), "t4c-ADR 확정 결정", now.Add(-200 * 24 * time.Hour), int32(0), int32(0), int32(0), ":decision:"},
		{int32(32), "t4c-버전 정책", now.Add(-10 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:"},
		{int32(33), "t4c-일반", now.Add(-90 * 24 * time.Hour), int32(0), int32(0), int32(0), ":vol:conditional:"},
		{int32(34), "t4c-평범", now.Add(-5 * 24 * time.Hour), int32(0), int32(0), int32(0), ":env:"},
	}
}

// TestMaintainAgeAuditVolSuggestion — the v1.22.0 T4 report attachment: each
// --age-audit row carries suggested_vol (the deterministic topic/tags mapping,
// CONTENT §4) unless the row already carries an approved vol:* tag — those
// show vol_tag instead (the approval regime: no re-proposal over an approved
// tier). The score column is untouched by the mapping (the distribution seal
// — the same 2-signal values as before T4, only new reference columns).
func TestMaintainAgeAuditVolSuggestion(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return ageAuditVolRowsFixture(now), nil
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
	if len(got.Items) != 4 {
		t.Fatalf("항목 수 = %d, want 4: %s", len(got.Items), out)
	}
	// order score DESC: 31(70) > 33(35) > 32(4) > 34(2) — unchanged by T4.
	wantScore := []int{70, 35, 4, 2}
	for i, w := range wantScore {
		if got.Items[i].Score != w {
			t.Errorf("items[%d].score = %d, want %d(스코어 불변 — 매핑 비가중)", i, got.Items[i].Score, w)
		}
	}
	byID := map[int]struct{ sug, tag string }{}
	for _, it := range got.Items {
		byID[it.ID] = struct{ sug, tag string }{it.SuggestedVol, it.VolTag}
	}
	if r := byID[31]; r.sug != "vol:immutable" || r.tag != "" {
		t.Errorf("id31 제안 = %+v, want suggested vol:immutable(ADR·decision 매칭)", r)
	}
	if r := byID[32]; r.sug != "vol:volatile" || r.tag != "" {
		t.Errorf("id32 제안 = %+v, want suggested vol:volatile(버전 매칭)", r)
	}
	// an already-approved vol:* row shows the EXISTING tag — no re-proposal.
	if r := byID[33]; r.tag != "vol:conditional" || r.sug != "" {
		t.Errorf("id33 = %+v, want vol_tag vol:conditional·제안 없음(승인제)", r)
	}
	if r := byID[34]; r.sug != "vol:conditional" || r.tag != "" {
		t.Errorf("id34 제안 = %+v, want suggested vol:conditional(미매칭 기본)", r)
	}
	// the full suggestion vocabulary stays inside the 3-tag dictionary.
	for _, it := range got.Items {
		switch it.SuggestedVol {
		case "", "vol:immutable", "vol:volatile", "vol:conditional":
		default:
			t.Errorf("사전 밖 제안 %q (id %d)", it.SuggestedVol, it.ID)
		}
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

// TestStaleScoredTTLOverride — the stale_ttl_days meta override re-scales
// the TTL component (T5 wiring): with 90, a 100-day-old row saturates
// (score 70, not 39) and the listing order follows.
func TestStaleScoredTTLOverride(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return staleScoredRowsFixture(now), nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"stale_ttl_days", "90"}}, nil
		}
		return nil, nil
	}}}
	items, err := listStaleScored(context.Background(), f, 0)
	if err != nil {
		t.Fatalf("listStaleScored: %v", err)
	}
	byID := map[int]staleScoredItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	// ttl 100 ≥ 90 → saturated TTL (70); ttl 200 also saturated (70·stable
	// ties keep the SQL order); the fresh 10d·corr3 row stays 34.
	if s := byID[13]; s.Score != 70 {
		t.Errorf("id13(TTL 100·포화 90) = %d, want 70(포화 조기 점등)", s.Score)
	}
	if s := byID[11]; s.Score != 70 {
		t.Errorf("id11(TTL 200) = %d, want 70", s.Score)
	}
	if s := byID[12]; s.Score != 38 {
		t.Errorf("id12 = %d, want 38(TTL 성분만 재조정: 10/90·corr 30)", s.Score)
	}
}

// ── v1.22.0 T3: age-audit "source changed" reference · --reaffirm · --recheck ──

// reaffirmFakeStore — fakeMaintStore + the ReaffirmStore write extension
// (the UPDATE literal lives in the store layer — captured here, the ops
// flow asserts the delegation, commit, and output).
type reaffirmFakeStore struct {
	*fakeMaintStore
	reaffirmCalls [][]int
	reaffirmErr   error
}

func (f *reaffirmFakeStore) Reaffirm(ctx context.Context, ids []int) (int64, error) {
	f.reaffirmCalls = append(f.reaffirmCalls, ids)
	if f.reaffirmErr != nil {
		return 0, f.reaffirmErr
	}
	return int64(len(ids)), nil
}

// runReaffirmArgs — runMaintainArgs is fixed to *fakeMaintStore, so a local
// helper for the extension wrapper (the limitCapturingStore convention).
func runReaffirmArgs(t *testing.T, f *reaffirmFakeStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestMaintainAgeAuditSourceChanged — the signal-⑤ reference column: with
// stamped vault rows (content_changed_at NOT NULL) the report carries the
// "source changed" count phrase; without stamps the field is 0 and the
// phrase absent. The score distribution is SEALED — the items bytes are
// identical whether or not vault sources changed (the G2 ruling: the
// reference column carries no weight).
func TestMaintainAgeAuditSourceChanged(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	newStore := func(changed int) *fakeMaintStore {
		return &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM knowledge"):
				return ageAuditRowsFixture(now), nil
			case strings.Contains(sql, "FROM vault_files"):
				return [][]any{{int32(changed)}}, nil
			}
			return nil, nil
		}}}
	}
	itemsOf := func(out string) string {
		start := strings.Index(out, `"items":`)
		end := strings.LastIndex(out, `,"summary":`)
		if start < 0 || end < 0 || end <= start {
			t.Fatalf("items 절출 실패: %s", out)
		}
		return out[start:end]
	}
	// with stamps — the phrase and the count surface.
	f := newStore(2)
	out, code := runMaintainArgs(t, f, "--age-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if !strings.Contains(out, `"source_changed":2`) {
		t.Fatalf("source_changed 카운트 부재: %s", out)
	}
	if !strings.Contains(out, "source changed") {
		t.Fatalf("source changed 문구 부재: %s", out)
	}
	sealed := itemsOf(out)

	// without stamps — 0 and no phrase; the items bytes identical (seal).
	out0, code := runMaintainArgs(t, newStore(0), "--age-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out0)
	}
	if !strings.Contains(out0, `"source_changed":0`) {
		t.Fatalf("source_changed 0 부재: %s", out0)
	}
	if strings.Contains(out0, "source changed") {
		t.Fatalf("스탬프 없는데 문구 존재: %s", out0)
	}
	if itemsOf(out0) != sealed {
		t.Fatalf("스코어 분포 봉인 위반:\n무변경=%s\n변경=%s", itemsOf(out0), sealed)
	}
	// the seal material itself — the 2-signal scores (70/35/4, order 21,23,22).
	if !strings.Contains(sealed, `"score":70`) || !strings.Contains(sealed, `"score":35`) || !strings.Contains(sealed, `"score":4`) {
		t.Fatalf("기존 점수 산출 변경 의심: %s", sealed)
	}
	// a vault-count read failure degrades quietly to 0 (§13 — a lost
	// observation must not fail the audit).
	fErr := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return ageAuditRowsFixture(now), nil
		case strings.Contains(sql, "FROM vault_files"):
			return nil, fmt.Errorf("vault lookup unavailable")
		}
		return nil, nil
	}}}
	out, code = runMaintainArgs(t, fErr, "--age-audit")
	if code != 0 || !strings.Contains(out, `"status":"age-audit"`) || !strings.Contains(out, `"source_changed":0`) {
		t.Fatalf("조회 실패 조용 강등 위반 exit=%d: %s", code, out)
	}
}

// TestMaintainReaffirmResetsValidity — the happy path: active targets get
// the Reaffirm delegation (valid_at=now() is the store layer's), the audit
// commit records the reaffirm attribution, and the output is the reaffirmed
// JSON. Both --id and --ids forms.
func TestMaintainReaffirmResetsValidity(t *testing.T) {
	shortRetry(t)
	newFake := func() *reaffirmFakeStore {
		return &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM knowledge") && strings.Contains(sql, "status") {
				return [][]any{{int32(7), "active"}, {int32(8), "active"}}, nil
			}
			return nil, nil
		}}}}
	}
	f := newFake()
	out, code := runReaffirmArgs(t, f, "--reaffirm", "--id", "7", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.reaffirmCalls) != 1 || len(f.reaffirmCalls[0]) != 1 || f.reaffirmCalls[0][0] != 7 {
		t.Fatalf("Reaffirm 호출 = %v, want [[7]]", f.reaffirmCalls)
	}
	if !strings.Contains(out, `"status":"reaffirmed"`) || !strings.Contains(out, `"count":1`) {
		t.Fatalf("출력 = %s", out)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "reaffirm") {
		t.Fatalf("감사 커밋 부재: %v", f.commits)
	}
	if len(f.invalidatedIDs) != 0 {
		t.Fatalf("무효화 경로 오염: %v", f.invalidatedIDs)
	}

	f = newFake()
	out, code = runReaffirmArgs(t, f, "--reaffirm", "--ids", "7,8", "--yes")
	if code != 0 {
		t.Fatalf("--ids exit = %d: %s", code, out)
	}
	if len(f.reaffirmCalls) != 1 || len(f.reaffirmCalls[0]) != 2 {
		t.Fatalf("--ids Reaffirm 호출 = %v, want [[7 8]]", f.reaffirmCalls)
	}
	if !strings.Contains(out, `"count":2`) {
		t.Fatalf("--ids 출력 = %s", out)
	}
}

// TestMaintainReaffirmGuards — the confirmation and target guards:
// without --yes a guidance error (exit 1, no write); without --id/--ids a
// usage error; both together rejected; a foreign value flag (--days)
// rejected by mode ownership; --yes alone under --recheck rejected.
func TestMaintainReaffirmGuards(t *testing.T) {
	base := func() *reaffirmFakeStore {
		return &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return nil, nil
		}}}}
	}
	// missing --yes — the confirmation guidance, no delegation, no commit.
	f := base()
	out, code := runReaffirmArgs(t, f, "--reaffirm", "--id", "7")
	if code != 1 || !strings.Contains(out, "--yes") {
		t.Fatalf("--yes 부재 exit=%d out=%s, want 1+안내", code, out)
	}
	if len(f.reaffirmCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("가드 전 쓰기 발생: %v %v", f.reaffirmCalls, f.commits)
	}
	// no target at all.
	if out, code = runReaffirmArgs(t, base(), "--reaffirm", "--yes"); code != 1 || !strings.Contains(out, "--id") {
		t.Fatalf("대상 부재 exit=%d out=%s", code, out)
	}
	// both --id and --ids — the spec's alternation is exclusive.
	if out, code = runReaffirmArgs(t, base(), "--reaffirm", "--id", "7", "--ids", "8", "--yes"); code != 1 {
		t.Fatalf("--id·--ids 병용 exit=%d out=%s, want 1", code, out)
	}
	// a foreign value flag is mode-ownership rejected (exit 1).
	if out, code = runReaffirmArgs(t, base(), "--reaffirm", "--id", "7", "--days", "5", "--yes"); code != 1 {
		t.Fatalf("무관 플래그 exit=%d out=%s, want 1", code, out)
	}
	// --yes is not valid under --recheck (read-only).
	if out, code = runReaffirmArgs(t, base(), "--recheck", "5", "--yes"); code != 1 {
		t.Fatalf("--recheck --yes exit=%d out=%s, want 1", code, out)
	}
	// an unknown flag stays exit 3 (the invalid-arg contract).
	if out, code = runReaffirmArgs(t, base(), "--reaffirm", "--wat"); code != 3 {
		t.Fatalf("unknown flag exit=%d out=%s, want 3", code, out)
	}
}

// TestMaintainReaffirmMissingTarget — a nonexistent or non-active id is
// exit 1 with the diagnostic, and no write fires (the target set is the
// active knowledge rows only).
func TestMaintainReaffirmMissingTarget(t *testing.T) {
	shortRetry(t)
	f := &reaffirmFakeStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			// 7 active, 8 invalidated, 99 absent.
			return [][]any{{int32(7), "active"}, {int32(8), "invalidated"}}, nil
		}
		return nil, nil
	}}}}
	out, code := runReaffirmArgs(t, f, "--reaffirm", "--ids", "7,8,99", "--yes")
	if code != 1 {
		t.Fatalf("exit = %d, want 1(대상 부재): %s", code, out)
	}
	if !strings.Contains(out, "99") || !strings.Contains(out, "8") {
		t.Fatalf("진단에 누락 대상 미포함: %s", out)
	}
	if len(f.reaffirmCalls) != 0 || len(f.commits) != 0 {
		t.Fatalf("부재 경로에서 쓰기: %v %v", f.reaffirmCalls, f.commits)
	}
}

// TestMaintainRecheckReport — the read-only recheck report: the conditions
// value, the vol dictionary tags, and the valid_at age ride along; a row
// without conditions gets the no-conditions guidance (conditions absent —
// T5 wires the write-side field).
func TestMaintainRecheckReport(t *testing.T) {
	validAt := time.Now().Add(-40 * 24 * time.Hour).UTC()
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{{"t3-재점검-데모", ":adr::vol:immutable:", "requires flint >= 1.22 (demo)", validAt, "demo/notes/t3-recheck.md"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--recheck", "5")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got struct {
		Status          string   `json:"status"`
		ID              int      `json:"id"`
		Topic           string   `json:"topic"`
		Conditions      string   `json:"conditions"`
		ConditionsEmpty bool     `json:"conditions_empty"`
		VolTags         []string `json:"vol_tags"`
		ValidAt         string   `json:"valid_at"`
		AgeDays         int      `json:"age_days"`
		SourcePath      string   `json:"source_path"`
		SourceCleared   int      `json:"source_cleared"`
		Summary         string   `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "recheck" || got.ID != 5 {
		t.Fatalf("status/id = %q/%d — %s", got.Status, got.ID, out)
	}
	if got.Conditions != "requires flint >= 1.22 (demo)" || got.ConditionsEmpty {
		t.Fatalf("conditions = %q(empty=%v)", got.Conditions, got.ConditionsEmpty)
	}
	if len(got.VolTags) != 1 || got.VolTags[0] != "vol:immutable" {
		t.Fatalf("vol_tags = %v, want [vol:immutable]", got.VolTags)
	}
	if got.AgeDays != 40 {
		t.Fatalf("age_days = %d, want 40", got.AgeDays)
	}
	if !strings.Contains(got.Summary, "conditions") {
		t.Fatalf("summary 안내 부재: %q", got.Summary)
	}
	// v1.24.0 T2 — the provenance fields ride along: the row's
	// source_path verbatim; source_cleared stays 0 on a store without
	// the clear extension (the report-only contract).
	if got.SourcePath != "demo/notes/t3-recheck.md" {
		t.Fatalf("source_path = %q, want 데모 경로", got.SourcePath)
	}
	if got.SourceCleared != 0 {
		t.Fatalf("source_cleared = %d, want 0(미구현 스토어 조용한 생략)", got.SourceCleared)
	}

	// a conditions-less row — the guidance variant.
	f2 := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{{"t3-무조건-데모", ":adr:", "", validAt}}, nil
		}
		return nil, nil
	}}}
	out, code = runMaintainArgs(t, f2, "--recheck", "6")
	if code != 0 {
		t.Fatalf("무조건 행 exit = %d: %s", code, out)
	}
	if !strings.Contains(out, `"conditions_empty":true`) || !strings.Contains(out, "no conditions") {
		t.Fatalf("conditions 부재 안내 부재: %s", out)
	}
}

// TestMaintainRecheckGuards — --recheck takes the id as its own value
// (the --expire-pending convention): a missing, non-numeric, or non-positive
// value is exit 1; a foreign value flag is mode-ownership rejected; a
// missing id is ErrNotFound exit 1.
func TestMaintainRecheckGuards(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // no rows — not found
	}}}
	for _, args := range [][]string{
		{"--recheck"},
		{"--recheck", "abc"},
		{"--recheck", "0"},
		{"--recheck", "-3"},
		{"--recheck", "5", "--limit", "3"},
	} {
		out, code := runMaintainArgs(t, f, args...)
		if code != 1 {
			t.Errorf("%v: exit=%d, want 1 — %s", args, code, out)
		}
	}
	// a missing knowledge row — exit 1 with the not-found diagnostic.
	out, code := runMaintainArgs(t, f, "--recheck", "99")
	if code != 1 || !strings.Contains(out, "not found") {
		t.Fatalf("부재 exit=%d out=%s, want 1+not found", code, out)
	}
}

// TestMaintainUsageLineMentionsReaffirm — the bare-usage guidance carries
// the two new entries (agent self-correction discovery path).
func TestMaintainUsageLineMentionsReaffirm(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f)
	if code != 3 {
		t.Fatalf("bare exit = %d, want 3", code)
	}
	for _, want := range []string{"--reaffirm", "--recheck"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage에 %s 부재: %s", want, out)
		}
	}
}

// TestMaintainRecheckAgeAuditRealSlow — slow (FLINT_TEST_DSN — the verify
// gate): on an isolated scratch DB, --recheck reads the real conditions
// column (set/empty), and --age-audit reports the real content_changed_at
// stamps (present→count+phrase, cleared→0) with the score bytes sealed
// across the stamp flip. Synthetic demo rows only.
func TestMaintainRecheckAgeAuditRealSlow(t *testing.T) {
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
	name := fmt.Sprintf("flint_t3recheck_%d", time.Now().UnixNano()%1e9)
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
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	const prefix = "t3-rck-"
	ids, err := ms.InsertBatch(ctx, []map[string]any{
		staleSeedRec(prefix+"cond", "t3-rck-a", "active", ":adr:"),
		staleSeedRec(prefix+"plain", "t3-rck-b", "active", ":adr:"),
	})
	if err != nil {
		t.Fatalf("시드: %v", err)
	}
	// one row carries conditions (T5 wires the write path — the column is
	// seeded directly here); both age 100d so they surface in age-audit.
	if _, err := ms.Query(ctx,
		`UPDATE knowledge SET conditions = $1, ingested_at = now() - interval '100 days' WHERE id = $2`,
		"demo condition: needs flint 1.22+ (synthetic)", ids[0]); err != nil {
		t.Fatalf("conditions 시드: %v", err)
	}
	if _, err := ms.Query(ctx,
		`UPDATE knowledge SET ingested_at = now() - interval '100 days' WHERE id = $1`, ids[1]); err != nil {
		t.Fatalf("경과 시드: %v", err)
	}
	// a stamped vault row (the T2 scan-diff stamp — seeded directly).
	if _, err := ms.Query(ctx,
		`INSERT INTO vault_files (path, title, tags, mtime, size, content_hash, content_changed_at)
		 VALUES ('demo/notes/t3-recheck-demo.md', 't3 demo', '', now(), 10, 't3-hash-demo', now())`); err != nil {
		t.Fatalf("vault 시드: %v", err)
	}
	run := func(args ...string) (string, int) {
		t.Helper()
		var out strings.Builder
		code := RunMaintain(ctx, ms, args, nil, &out)
		return out.String(), code
	}
	// --recheck: the conditions value surfaces; the empty row gets the guidance.
	out, code := run("--recheck", fmt.Sprint(ids[0]))
	if code != 0 || !strings.Contains(out, "demo condition: needs flint 1.22+ (synthetic)") {
		t.Fatalf("recheck(conditions) exit=%d out=%s", code, out)
	}
	out, code = run("--recheck", fmt.Sprint(ids[1]))
	if code != 0 || !strings.Contains(out, `"conditions_empty":true`) {
		t.Fatalf("recheck(무조건) exit=%d out=%s", code, out)
	}
	// --age-audit with the stamp — count 1 + phrase; items sealed.
	out, code = run("--age-audit")
	if code != 0 || !strings.Contains(out, `"source_changed":1`) || !strings.Contains(out, "source changed") {
		t.Fatalf("age-audit(스탬프) exit=%d out=%s", code, out)
	}
	sealed := out[strings.Index(out, `"items":`):strings.LastIndex(out, `,"summary":`)]
	// clear the stamp (the hash-restored NULL) — 0, no phrase, same items bytes.
	if _, err := ms.Query(ctx,
		`UPDATE vault_files SET content_changed_at = NULL WHERE path = 'demo/notes/t3-recheck-demo.md'`); err != nil {
		t.Fatalf("스탬프 소거: %v", err)
	}
	out, code = run("--age-audit")
	if code != 0 || !strings.Contains(out, `"source_changed":0`) || strings.Contains(out, "source changed") {
		t.Fatalf("age-audit(소거) exit=%d out=%s", code, out)
	}
	if got := out[strings.Index(out, `"items":`):strings.LastIndex(out, `,"summary":`)]; got != sealed {
		t.Fatalf("스코어 분포 봉인 위반(실DB):\n소거=%s\n스탬프=%s", got, sealed)
	}
}

// TestMaintainAgeAuditThresholdOverride — the stale_score_threshold meta
// override is reflected in the report threshold field and the marking
// (with 30, the score-35 row flips to marked; the score-4 row stays
// unmarked — the default 70 would leave it unmarked).
func TestMaintainAgeAuditThresholdOverride(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge"):
			return ageAuditRowsFixture(now), nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"stale_score_threshold", "30"}}, nil
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
	if got.Threshold != 30 {
		t.Fatalf("threshold = %d, want 30(오버라이드)", got.Threshold)
	}
	byID := map[int]int{}
	marked := map[int]bool{}
	for _, it := range got.Items {
		byID[it.ID] = it.Score
		marked[it.ID] = it.OverThreshold
	}
	if !marked[21] || !marked[23] || marked[22] {
		t.Errorf("마킹(임계 30) = %v — want 21·23 마킹, 22 미마킹(점수 %v)", marked, byID)
	}
}

// ── v1.24.0 T2 — --recheck automatically clears the matched vault
// source-change stamps (the content_changed_at clearing of vault_files
// rows whose path equals the knowledge row's source_path). ──

// recheckClearStore — a fakeMaintStore wrapper implementing the two
// recheck write extensions (the clear recorder + the conditions
// backfill), so the report·backfill paths exercise the clear.
type recheckClearStore struct {
	*fakeMaintStore
	cleared    []string // the paths passed to ClearVaultSourceChanged
	clearN     int64    // the rows-affected answer
	setCondN   int64    // SetRecheckConditions rows-affected answer
	conditions []string
}

func (s *recheckClearStore) ClearVaultSourceChanged(ctx context.Context, path string) (int64, error) {
	s.cleared = append(s.cleared, path)
	return s.clearN, nil
}

func (s *recheckClearStore) SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error) {
	s.conditions = append(s.conditions, conditions)
	return s.setCondN, nil
}

// runRecheckClear — runMaintainArgs is fixed to *fakeMaintStore, so a
// local runner for the wrapper (the TUI-test precedent).
func runRecheckClear(t *testing.T, s *recheckClearStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), s, args, nil, &out)
	return out.String(), code
}

// TestMaintainRecheckClearsVaultSource — every --recheck invocation
// (report form·--conditions form alike — the --conditions argument is
// optional for the clear) clears the vault rows whose path equals the
// target row's source_path. Multi-share implication: the clear is keyed
// on the vault path, so every knowledge row sharing that source_path has
// its recheck queue consumed by the one call — the report surfaces the
// cleared count.
func TestMaintainRecheckClearsVaultSource(t *testing.T) {
	validAt := time.Now().Add(-10 * 24 * time.Hour).UTC()
	// mk — the report row (5 cells, source_path at index 4) + the clear
	// helper's own source_path lookup, served per scenario path.
	mk := func(path any, report bool) *fakeStore {
		return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "topic, tags, conditions, valid_at"):
				if !report {
					return nil, nil
				}
				return [][]any{{"t2-소거-데모", ":demo:", "requires demo", validAt, path}}, nil
			case strings.Contains(sql, "SELECT source_path FROM knowledge"):
				if path == nil {
					return nil, nil
				}
				return [][]any{{path}}, nil
			case strings.Contains(sql, "SELECT id FROM knowledge"):
				return [][]any{{int32(8)}}, nil // the backfill precheck target
			}
			return nil, nil
		}}
	}
	// the report form: a row carrying source_path → one clear call keyed
	// on the path, the count rides the report, and the clear gets its own
	// commit (a write happened — the zero-row commit avoidance).
	s := &recheckClearStore{fakeMaintStore: &fakeMaintStore{fakeStore: mk("demo/notes/v124-t2.md", true)}, clearN: 2}
	out, code := runRecheckClear(t, s, "--recheck", "7")
	if code != 0 {
		t.Fatalf("report exit=%d out=%s", code, out)
	}
	if len(s.cleared) != 1 || s.cleared[0] != "demo/notes/v124-t2.md" {
		t.Fatalf("clear 호출=%v, want [demo/notes/v124-t2.md] 1회", s.cleared)
	}
	if !strings.Contains(out, `"source_cleared":2`) || !strings.Contains(out, `"source_path":"demo/notes/v124-t2.md"`) {
		t.Fatalf("보고 필드 부재: %s", out)
	}
	if n := len(s.fakeStore.commits); n != 1 {
		t.Fatalf("clear 커밋=%d회, want 1", n)
	}
	// the --conditions form also clears (the same invocation contract).
	c := &recheckClearStore{fakeMaintStore: &fakeMaintStore{fakeStore: mk("demo/notes/v124-t2-b.md", false)}, clearN: 1, setCondN: 1}
	out, code = runRecheckClear(t, c, "--recheck", "8", "--conditions", "demo 조건 (합성)", "--yes")
	if code != 0 {
		t.Fatalf("backfill exit=%d out=%s", code, out)
	}
	if len(c.cleared) != 1 || c.cleared[0] != "demo/notes/v124-t2-b.md" {
		t.Fatalf("backfill clear 호출=%v", c.cleared)
	}
	if !strings.Contains(out, `"source_cleared":1`) {
		t.Fatalf("backfill 보고에 source_cleared 부재: %s", out)
	}
	// a row without source_path (NULL) clears nothing — no provenance
	// link, no write, no commit.
	n := &recheckClearStore{fakeMaintStore: &fakeMaintStore{fakeStore: mk(nil, true)}, clearN: 5}
	out, code = runRecheckClear(t, n, "--recheck", "9")
	if code != 0 {
		t.Fatalf("무경로 exit=%d out=%s", code, out)
	}
	if len(n.cleared) != 0 || len(n.fakeStore.commits) != 0 {
		t.Fatalf("무경로 소거/커밋 발생: cleared=%v commits=%d", n.cleared, len(n.fakeStore.commits))
	}
	if !strings.Contains(out, `"source_cleared":0`) {
		t.Fatalf("무경로 보고 필드 부재: %s", out)
	}
}

// ── v1.24.0 T2 slow — the --recheck vault source-change clear on the
// real Doltgres path (dedicated scratch DB created and dropped here —
// never a shared or production DB). ──

// slowMaintStore extensions for the recheck clear scenario: the clear
// UPDATE (the RecheckSourceClearStore extension — the same statement the
// production realStore carries in cli/maintain.go) and the conditions
// backfill (RecheckConditionsStore — the SetRecheckConditions
// convention). Test adapters: the literals mirror the store layer.
func (s *slowMaintStore) ClearVaultSourceChanged(ctx context.Context, path string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE vault_files SET content_changed_at = NULL
		 WHERE path = $1 AND content_changed_at IS NOT NULL`, path)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *slowMaintStore) SetRecheckConditions(ctx context.Context, id int, conditions string) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE knowledge SET conditions = $2
		 WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`, id, conditions)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// TestMaintainRecheckSourceClearRealSlow — on an isolated scratch DB:
// `--recheck N` (report form and --conditions form alike) clears the
// content_changed_at stamps of the vault_files rows whose path equals
// the target's source_path; the multi-share implication holds (a second
// knowledge row on the same path finds the queue already consumed), and
// an unrelated path's stamp survives.
func TestMaintainRecheckSourceClearRealSlow(t *testing.T) {
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
	name := fmt.Sprintf("flint_v1240t2_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
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
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	const (
		pathA = "demo/notes/v124-t2-a.md"
		pathB = "demo/notes/v124-t2-b.md"
	)
	ids, err := ms.InsertBatch(ctx, []map[string]any{
		staleSeedRec("v124-t2-공유-a", "v124-t2-ha", "active", ":demo:"),
		staleSeedRec("v124-t2-공유-b", "v124-t2-hb", "active", ":demo:"),
		staleSeedRec("v124-t2-단독-c", "v124-t2-hc", "active", ":demo:"),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, id := range ids {
		if _, err := ms.Query(ctx, `UPDATE knowledge SET valid_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("valid_at seed: %v", err)
		}
	}
	// A·B share pathA; C carries pathB. Both vault rows are stamped.
	seedPath := func(id int, path string) {
		t.Helper()
		if _, err := ms.Query(ctx,
			`UPDATE knowledge SET source_path = $2 WHERE id = $1`, id, path); err != nil {
			t.Fatalf("source_path seed(%d): %v", id, err)
		}
	}
	seedPath(ids[0], pathA)
	seedPath(ids[1], pathA)
	seedPath(ids[2], pathB)
	seedVault := func(path string) {
		t.Helper()
		if _, err := ms.Query(ctx,
			`INSERT INTO vault_files (path, title, tags, mtime, size, content_hash, content_changed_at)
			 VALUES ($1, 'v124 t2 demo', '', now(), 10, $2, now())`, path, "v124-hash-"+path); err != nil {
			t.Fatalf("vault seed(%s): %v", path, err)
		}
	}
	seedVault(pathA)
	seedVault(pathB)
	stamped := func(path string) bool {
		rows, err := ms.Query(ctx,
			`SELECT content_changed_at IS NOT NULL FROM vault_files WHERE path = $1`, path)
		if err != nil || len(rows) != 1 {
			t.Fatalf("stamp lookup(%s): %v %v", path, rows, err)
		}
		return fmt.Sprint(rows[0][0]) == "true"
	}
	run := func(args ...string) (string, int) {
		t.Helper()
		var out strings.Builder
		code := RunMaintain(ctx, ms, args, nil, &out)
		return out.String(), code
	}
	// the report form clears the matched path only.
	out, code := run("--recheck", fmt.Sprint(ids[0]))
	if code != 0 || !strings.Contains(out, `"source_cleared":1`) || !strings.Contains(out, pathA) {
		t.Fatalf("report 소거 exit=%d out=%s", code, out)
	}
	if stamped(pathA) {
		t.Fatalf("pathA 스탬프 미소거")
	}
	if !stamped(pathB) {
		t.Fatalf("무관 경로 pathB 스탬프가 소거됨 — 연결 축 위반")
	}
	// the multi-share implication: the sibling row on the same path finds
	// the queue consumed (source_cleared 0, no error).
	out, code = run("--recheck", fmt.Sprint(ids[1]))
	if code != 0 || !strings.Contains(out, `"source_cleared":0`) {
		t.Fatalf("공유 재점검 exit=%d out=%s", code, out)
	}
	// re-stamp pathB, then the --conditions form clears it too (every
	// --recheck invocation clears — the --conditions argument is
	// optional).
	if _, err := ms.Query(ctx,
		`UPDATE vault_files SET content_changed_at = now() WHERE path = $1`, pathB); err != nil {
		t.Fatalf("pathB 재스탬프: %v", err)
	}
	out, code = run("--recheck", fmt.Sprint(ids[2]), "--conditions", "v124 t2 demo condition", "--yes")
	if code != 0 || !strings.Contains(out, `"source_cleared":1`) {
		t.Fatalf("backfill 소거 exit=%d out=%s", code, out)
	}
	if stamped(pathB) {
		t.Fatalf("pathB 스탬프 미소거(backfill 형태)")
	}
	// the conditions write itself round-tripped (the r96 contract kept).
	rows, err := ms.Query(ctx, `SELECT conditions FROM knowledge WHERE id = $1`, ids[2])
	if err != nil || len(rows) != 1 || fmt.Sprint(rows[0][0]) != "v124 t2 demo condition" {
		t.Fatalf("conditions 왕복: %v %v", rows, err)
	}
}

// ── v1.24.0 T3: --merge-audit (the duplicate-pair report — listing only) ──

// mergeAuditScanRowsFixture — the --merge-audit scan source rows
// (synthetic demo): an identical pair (1.0), the exact-0.6 boundary pair
// (the knowledge-package fixture strings), and a below-threshold row.
func mergeAuditScanRowsFixture() [][]any {
	return [][]any{
		{int32(1), "데모 배포 절차", "매주 화요일 배포 프리즈와 롤백 점검을 함께 기록한다"},
		{int32(2), "데모 배포 절차", "매주 화요일 배포 프리즈와 롤백 점검을 함께 기록한다"},
		{int32(3), "a", "aabc"},
		{int32(4), "a", "aaxy"},
		{int32(5), "a", "zzzz"},
	}
}

// mergeAuditJSON — for parsing the --merge-audit output.
type mergeAuditJSON struct {
	Status        string  `json:"status"`
	Threshold     float64 `json:"threshold"`
	ScanCap       int     `json:"scan_cap"`
	Scanned       int     `json:"scanned"`
	ScanTruncated bool    `json:"scan_truncated"`
	Cost          struct {
		PairsTotal      int    `json:"pairs_total"`
		MicrosPerPair   int    `json:"micros_per_pair"`
		EstimatedMicros int    `json:"estimated_micros"`
		BudgetMicros    int    `json:"budget_micros"`
		OverBudget      bool   `json:"over_budget"`
		AdvisedCap      int    `json:"advised_cap"`
		EffectiveRows   int    `json:"effective_rows"`
		Advice          string `json:"advice"`
	} `json:"cost"`
	EchoP95US int `json:"echo_scan_p95_us"`
	Count     int `json:"count"`
	Items     []struct {
		A        int     `json:"a"`
		B        int     `json:"b"`
		TopicA   string  `json:"topic_a"`
		TopicB   string  `json:"topic_b"`
		Score    float64 `json:"score"`
		Relation string  `json:"relation"`
	} `json:"items"`
	Summary string `json:"summary"`
}

// mergeAuditFake — the standard --merge-audit fake: the scan rows ride
// queryFn (the active-scan SQL — status='active' AND invalid_at IS
// NULL, the v1.24.1 strict active fetch), everything else (meta loads,
// the counter upsert) returns empty and records its args.
func mergeAuditFake(t *testing.T) (*fakeMaintStore, *[]string) {
	t.Helper()
	var metaWrites []string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "invalid_at IS NULL AND status = 'active' LIMIT") {
			return mergeAuditScanRowsFixture(), nil
		}
		if strings.Contains(sql, "INSERT INTO meta") {
			if len(args) > 0 {
				if k, ok := args[0].(string); ok {
					metaWrites = append(metaWrites, k)
				}
			}
			return nil, nil
		}
		return nil, nil // meta reads (thresholds, scan_stats) — defaults
	}}}
	return f, &metaWrites
}

// TestMaintainMergeAuditList — the report contract: the synthetic similar
// pair (1.0) and the exact-0.6 boundary pair list at the default
// conflict_dup_threshold, sorted score-descending; the threshold, scan
// cap (suggest_scan_cap), scanned count, and the measured pre-cost
// estimate are all published; the disposition guidance names the
// approval circuit.
func TestMaintainMergeAuditList(t *testing.T) {
	f, _ := mergeAuditFake(t)
	out, code := runMaintainArgs(t, f, "--merge-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got mergeAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "merge-audit" || got.Count != 2 || len(got.Items) != 2 {
		t.Fatalf("status/count/items = %q/%d/%d — %s", got.Status, got.Count, len(got.Items), out)
	}
	if got.Items[0].A != 1 || got.Items[0].B != 2 || got.Items[0].Score < 0.999 {
		t.Errorf("items[0] = %+v, want (1,2) 동일 행 1.0", got.Items[0])
	}
	if got.Items[1].A != 3 || got.Items[1].B != 4 {
		t.Errorf("items[1] = %+v, want (3,4) 경계 0.6", got.Items[1])
	}
	if got.Items[1].Relation != "duplicates" {
		t.Errorf("relation = %q, want duplicates(EchoScan 분류 준용)", got.Items[1].Relation)
	}
	if got.ScanCap != 5000 || got.Scanned != 5 || got.ScanTruncated {
		t.Errorf("scan_cap/scanned/truncated = %d/%d/%v, want 5000/5/false", got.ScanCap, got.Scanned, got.ScanTruncated)
	}
	if got.Cost.PairsTotal != 10 || got.Cost.MicrosPerPair <= 0 {
		t.Errorf("cost = %+v, want pairs_total 10·실측 µs/쌍 > 0", got.Cost)
	}
	if got.Cost.EstimatedMicros != got.Cost.MicrosPerPair*got.Cost.PairsTotal {
		t.Errorf("estimated_micros = %d, want per×total", got.Cost.EstimatedMicros)
	}
	if !strings.Contains(got.Summary, "dispose via maintain --merge/--supersede (approval required)") {
		t.Errorf("요약에 처분 안내 부재: %q", got.Summary)
	}
	joined := strings.Join(f.queries, ";")
	if !strings.Contains(joined, "invalid_at IS NULL AND status = 'active' LIMIT 5001") {
		t.Errorf("스캔 SQL에 상한 5000+1 부재: %q", joined)
	}
}

// TestMaintainMergeAuditListingOnly — the report never disposes: no
// state-transition method fires, no knowledge write SQL runs, no dolt
// commit is made (the decay-audit convention — disposition is delegated
// to --merge/--supersede with approval).
func TestMaintainMergeAuditListingOnly(t *testing.T) {
	f, _ := mergeAuditFake(t)
	out, code := runMaintainArgs(t, f, "--merge-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.invalidatedIDs) > 0 || len(f.merged) > 0 || len(f.superseded) > 0 {
		t.Errorf("처분 메서드 발화: invalidate=%v merge=%v supersede=%v",
			f.invalidatedIDs, f.merged, f.superseded)
	}
	if len(f.commits) > 0 {
		t.Errorf("dolt commit 발화: %v", f.commits)
	}
	joined := strings.Join(f.queries, ";")
	for _, write := range []string{"UPDATE knowledge", "DELETE FROM knowledge", "maintenance_runs"} {
		if strings.Contains(joined, write) {
			t.Errorf("쓰기 SQL %q 발화(리스팅만 위반): %q", write, joined)
		}
	}
}

// TestMaintainMergeAuditLimit — --limit caps the listing (the top-scored
// pair survives, the decay-audit convention).
func TestMaintainMergeAuditLimit(t *testing.T) {
	f, _ := mergeAuditFake(t)
	out, code := runMaintainArgs(t, f, "--merge-audit", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got mergeAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Count != 1 || len(got.Items) != 1 || got.Items[0].A != 1 {
		t.Fatalf("상한 적용 결과 = %d건 %+v, want 1건 (1,2)", got.Count, got.Items)
	}
}

// TestMaintainMergeAuditCounter — the merge_audit_pairs curation counter
// fires once per run with the listed count (the D-series metering
// convention — the counter key derives from the registry's curation
// group).
func TestMaintainMergeAuditCounter(t *testing.T) {
	f, writes := mergeAuditFake(t)
	out, code := runMaintainArgs(t, f, "--merge-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	hit := 0
	for _, k := range *writes {
		if strings.HasPrefix(k, "daily:merge_audit_pairs:") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("merge_audit_pairs 카운터 적립 %d회, want 1(리스팅 2건 1회 적립): %v", hit, *writes)
	}
}

// TestMaintainMergeAuditScanCapOverride — the scan bound prefers the
// meta suggest_scan_cap override (LIMIT cap+1 follows it).
func TestMaintainMergeAuditScanCapOverride(t *testing.T) {
	var gotSQL string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "invalid_at IS NULL AND status = 'active' LIMIT") {
			gotSQL = sql
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta") && strings.Contains(sql, "SELECT") {
			return [][]any{{"suggest_scan_cap", "7"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--merge-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if !strings.Contains(gotSQL, "LIMIT 8") {
		t.Fatalf("오버라이드 스캔 SQL = %q, want LIMIT 8(7+1)", gotSQL)
	}
}

// TestMaintainMergeAuditRejectsForeignFlags — mode-ownership validation:
// the read-only report rejects unrelated value flags (--days) with exit 1.
func TestMaintainMergeAuditRejectsForeignFlags(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--merge-audit", "--days", "5")
	if code != 1 {
		t.Fatalf("exit = %d, want 1(무관 플래그 거부): %s", code, out)
	}
}

// TestMaintainMergeAuditRealSlow — slow (FLINT_TEST_DSN — the verify-gate
// DB): synthetic seeds on an isolated scratch DB, the real path lists the
// seeded duplicate pair with the published cost estimate, and the run
// leaves every row active (listing only — no disposition, no commit).
func TestMaintainMergeAuditRealSlow(t *testing.T) {
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
	name := fmt.Sprintf("flint_t3merge_%d", time.Now().UnixNano()%1e9)
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
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	topic := "t3-merge-audit 데모 배포 절차"
	summary := "매주 화요일 배포 프리즈와 롤백 점검을 함께 기록한다"
	seed := func(hash, t, s string) map[string]any {
		return map[string]any{
			"kind": "knowledge", "topic": t, "summary": s, "tags": ":adr:",
			"confidence": 0.9, "content_hash": hash, "status": "active",
		}
	}
	if _, err := ms.InsertBatch(ctx, []map[string]any{
		seed("t3-ma-a", topic, summary),
		seed("t3-ma-b", topic, summary), // identical text -> Dice 1.0
		// a genuinely dissimilar row (the staleSeedRec hash-summary shape
		// scores high on short strings — measured on the real path), so
		// exactly one pair lists.
		seed("t3-ma-c", "t3-merge-audit 무관 주제",
			"지난 분기 독서 목록 정리와 서평 작성 회고를 남긴다"),
	}); err != nil {
		t.Fatalf("시드: %v", err)
	}
	var out strings.Builder
	if code := RunMaintain(ctx, ms, []string{"--merge-audit"}, nil, &out); code != 0 {
		t.Fatalf("--merge-audit exit = %d: %s", code, out.String())
	}
	var got mergeAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out.String())
	}
	if got.Status != "merge-audit" || got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("status/count = %q/%d — %s", got.Status, got.Count, out.String())
	}
	if got.Items[0].Score < 0.999 || got.Items[0].TopicA != topic {
		t.Errorf("쌍 = %+v, want 동일 행 1.0", got.Items[0])
	}
	if got.Cost.PairsTotal != 3 || got.Cost.MicrosPerPair <= 0 {
		t.Errorf("cost = %+v, want pairs_total 3(3행)·실측 µs/쌍", got.Cost)
	}
	// listing only: every seeded row stays active, nothing invalidated.
	rows, err := ms.Query(ctx, `SELECT count(*) FROM knowledge WHERE invalid_at IS NULL`)
	if err != nil || len(rows) == 0 {
		t.Fatalf("활성 카운트 조회: %v", err)
	}
	if n, _ := rows[0][0].(int64); n != 3 {
		t.Fatalf("활성 행 = %d, want 3(처분 미수행)", n)
	}
	// the curation counter landed in meta (the registry whitelist admits
	// merge_audit_pairs on the real path too).
	metaRows, err := ms.Query(ctx,
		`SELECT value FROM meta WHERE key LIKE 'daily:merge_audit_pairs:%'`)
	if err != nil {
		t.Fatalf("카운터 조회: %v", err)
	}
	if len(metaRows) == 0 {
		t.Fatalf("merge_audit_pairs 카운터 미적립: %s", out.String())
	}
}

// ── v1.24.0 T4: --recall-audit (the bad-recall feedback report — listing only) ──

// recallAuditJSON — for parsing the --recall-audit output.
type recallAuditJSON struct {
	Status string `json:"status"`
	MinBad int    `json:"min_bad"`
	Count  int    `json:"count"`
	Items  []struct {
		ID        int    `json:"id"`
		Topic     string `json:"topic"`
		BadRecall int    `json:"bad_recall"`
	} `json:"items"`
	Summary string `json:"summary"`
}

// recallAuditRowsFixture — the --recall-audit listing rows (synthetic demo):
// a 3-bad-recall row, the exact-2 boundary row (>= recall_audit_min_bad
// default 2), and a below-threshold 1 row (server-side filtered in reality —
// carried here only as the parse-shape reference).
func recallAuditRowsFixture() [][]any {
	return [][]any{
		{int32(11), "t4-recall-audit 데모 배포 절차", int64(3)},
		{int32(12), "t4-recall-audit 회고 리듬", int64(2)},
	}
}

// recallAuditFake — the standard --recall-audit fake: the listing rows ride
// queryFn (the bad-recall listing SQL), everything else (meta loads, the
// counter upsert) returns empty and records its meta-write keys.
func recallAuditFake(t *testing.T, rows [][]any) (*fakeMaintStore, *[]string) {
	t.Helper()
	var metaWrites []string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "AS bad_recall") && strings.Contains(sql, "ORDER BY") {
			return rows, nil
		}
		if strings.Contains(sql, "INSERT INTO meta") {
			if len(args) > 0 {
				if k, ok := args[0].(string); ok {
					metaWrites = append(metaWrites, k)
				}
			}
			return nil, nil
		}
		return nil, nil // meta reads (thresholds) — defaults
	}}}
	return f, &metaWrites
}

// TestMaintainRecallAuditList — the report contract: the synthetic
// bad-recall rows list score-descending (bad_recall DESC, id ASC) with the
// threshold published as min_bad (default 2); the listing SQL carries the
// server-side floor (>= $1) on the same bad-recall subquery the age-audit
// reference column uses; the summary names the approval circuit.
func TestMaintainRecallAuditList(t *testing.T) {
	f, _ := recallAuditFake(t, recallAuditRowsFixture())
	var gotArgs []any
	orig := f.queryFn
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "AS bad_recall") && strings.Contains(sql, "ORDER BY") {
			gotArgs = args
		}
		return orig(sql, args)
	}
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got recallAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Status != "recall-audit" || got.MinBad != 2 || got.Count != 2 || len(got.Items) != 2 {
		t.Fatalf("status/min_bad/count/items = %q/%d/%d/%d — %s", got.Status, got.MinBad, got.Count, len(got.Items), out)
	}
	if got.Items[0].ID != 11 || got.Items[0].BadRecall != 3 {
		t.Errorf("items[0] = %+v, want (11, bad 3) 점수 내림차순", got.Items[0])
	}
	if got.Items[1].ID != 12 || got.Items[1].BadRecall != 2 {
		t.Errorf("items[1] = %+v, want (12, bad 2) 경계 포함(>= 2)", got.Items[1])
	}
	if !strings.Contains(got.Summary, "dispose via maintain --invalidate/--supersede/--reaffirm (approval required)") {
		t.Errorf("요약에 처분 안내 부재: %q", got.Summary)
	}
	joined := strings.Join(f.queries, ";")
	for _, want := range []string{"AS bad_recall", ">= $1", "recall_events"} {
		if !strings.Contains(joined, want) {
			t.Errorf("리스팅 SQL에 %q 부재: %q", want, joined)
		}
	}
	if len(gotArgs) != 2 {
		t.Fatalf("리스팅 파라미터 %v, want [min_bad, limit] 2개", gotArgs)
	}
	if minBad, ok := gotArgs[0].(int); !ok || minBad != 2 {
		t.Errorf("임계 파라미터 = %v, want 2(기본 recall_audit_min_bad)", gotArgs[0])
	}
}

// TestMaintainRecallAuditLimit — --limit caps the listing (the decay-audit
// convention; the Go-side truncation is the defense over the server LIMIT).
func TestMaintainRecallAuditLimit(t *testing.T) {
	f, _ := recallAuditFake(t, recallAuditRowsFixture())
	out, code := runMaintainArgs(t, f, "--recall-audit", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var got recallAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out)
	}
	if got.Count != 1 || len(got.Items) != 1 || got.Items[0].ID != 11 {
		t.Fatalf("상한 적용 결과 = %d건 %+v, want 1건 (11)", got.Count, got.Items)
	}
}

// TestMaintainRecallAuditListingOnly — the report never disposes: no
// state-transition method fires, no knowledge write SQL runs, no dolt
// commit is made (the decay-audit convention — disposition is delegated to
// the approval circuit).
func TestMaintainRecallAuditListingOnly(t *testing.T) {
	f, _ := recallAuditFake(t, recallAuditRowsFixture())
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.invalidatedIDs) > 0 || len(f.merged) > 0 || len(f.superseded) > 0 {
		t.Errorf("처분 메서드 발화: invalidate=%v merge=%v supersede=%v",
			f.invalidatedIDs, f.merged, f.superseded)
	}
	if len(f.commits) > 0 {
		t.Errorf("dolt commit 발화: %v", f.commits)
	}
	joined := strings.Join(f.queries, ";")
	for _, write := range []string{"UPDATE knowledge", "DELETE FROM knowledge", "maintenance_runs"} {
		if strings.Contains(joined, write) {
			t.Errorf("쓰기 SQL %q 발화(리스팅만 위반): %q", write, joined)
		}
	}
}

// TestMaintainRecallAuditCounter — the recall_audit_listed curation counter
// fires once per run with the listed count (the D-series metering
// convention — the counter key derives from the registry's curation group).
func TestMaintainRecallAuditCounter(t *testing.T) {
	f, writes := recallAuditFake(t, recallAuditRowsFixture())
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	hit := 0
	for _, k := range *writes {
		if strings.HasPrefix(k, "daily:recall_audit_listed:") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("recall_audit_listed 카운터 적립 %d회, want 1(리스팅 1회 적립): %v", hit, *writes)
	}
}

// TestMaintainRecallAuditThresholdOverride — the listing floor prefers the
// meta recall_audit_min_bad override (the $1 parameter follows it).
func TestMaintainRecallAuditThresholdOverride(t *testing.T) {
	var gotArgs []any
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "AS bad_recall") && strings.Contains(sql, "ORDER BY") {
			gotArgs = args
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta") && strings.Contains(sql, "SELECT") {
			return [][]any{{"recall_audit_min_bad", "3"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--recall-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(gotArgs) == 0 {
		t.Fatalf("리스팅 쿼리 미발화: %s", out)
	}
	if minBad, ok := gotArgs[0].(int); !ok || minBad != 3 {
		t.Errorf("임계 파라미터 = %v, want 3(오버라이드)", gotArgs[0])
	}
}

// TestMaintainRecallAuditRejectsForeignFlags — mode-ownership validation:
// the read-only report rejects unrelated value flags (--days) with exit 1.
func TestMaintainRecallAuditRejectsForeignFlags(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--recall-audit", "--days", "5")
	if code != 1 {
		t.Fatalf("exit = %d, want 1(무관 플래그 거부): %s", code, out)
	}
}

// recallAuditDisposedFake — the --invalidate --ids approval-path fake: the
// existence check admits id 30, the qualify query returns the target's
// bad-recall count, and the counter upsert records its meta-write keys.
func recallAuditDisposedFake(t *testing.T, bad int64) (*fakeMaintStore, *[]string) {
	t.Helper()
	var metaWrites []string
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "WHERE id = ANY($1)") && !strings.Contains(sql, "AS bad_recall") {
			return [][]any{{int32(30)}}, nil
		}
		if strings.Contains(sql, "AS bad_recall") {
			return [][]any{{int32(30), bad}}, nil
		}
		if strings.Contains(sql, "INSERT INTO meta") {
			if len(args) > 0 {
				if k, ok := args[0].(string); ok {
					metaWrites = append(metaWrites, k)
				}
			}
			return nil, nil
		}
		return nil, nil
	}}}
	return f, &metaWrites
}

// TestMaintainRecallAuditDisposedCounter — the D4 disposed counter rides the
// approval path: --invalidate --ids of a listing-qualified target (bad 3 >=
// min 2) accrues recall_audit_disposed; a below-threshold target (bad 1)
// does not (the disposition still succeeds either way — the observation is
// quiet §13).
func TestMaintainRecallAuditDisposedCounter(t *testing.T) {
	f, writes := recallAuditDisposedFake(t, 3)
	out, code := runMaintainArgs(t, f, "--invalidate", "--ids", "30", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	hit := 0
	for _, k := range *writes {
		if strings.HasPrefix(k, "daily:recall_audit_disposed:") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("recall_audit_disposed 카운터 적립 %d회, want 1(적격 처분 1건): %v", hit, *writes)
	}

	f2, writes2 := recallAuditDisposedFake(t, 1)
	out2, code2 := runMaintainArgs(t, f2, "--invalidate", "--ids", "30", "--yes")
	if code2 != 0 {
		t.Fatalf("비적격 exit = %d: %s", code2, out2)
	}
	for _, k := range *writes2 {
		if strings.HasPrefix(k, "daily:recall_audit_disposed:") {
			t.Errorf("비적격(1 < 2) 처분에 disposed 카운터 적립: %v", *writes2)
		}
	}
}

// TestMaintainMergeDisposedCounterV1241 — v1.24.1 (r104): the --merge
// path accrues the disposed counter for a listing-qualified FROM (the
// merge-audit summary delegates disposal to --merge/--supersede alike —
// the D4 alignment; the pre-patch --merge never accrued).
func TestMaintainMergeDisposedCounterV1241(t *testing.T) {
	f, writes := recallAuditDisposedFake(t, 3)
	out, code := runMaintainArgs(t, f, "--merge", "--from", "30", "--to", "31")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if len(f.merged) != 1 || f.merged[0] != [2]int{30, 31} {
		t.Fatalf("merge 수행 = %v, want (30,31)", f.merged)
	}
	hit := 0
	for _, k := range *writes {
		if strings.HasPrefix(k, "daily:recall_audit_disposed:") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("recall_audit_disposed 카운터 적립 %d회, want 1(--merge 처분 계상): %v", hit, *writes)
	}
}

// TestMaintainRecallAuditLimitInt4CeilingV1241 — v1.24.1 (r105-2): a
// --limit past the int4 range is refused at parse (exit 3, the --days
// ceiling convention) before any audit query fires — the pre-patch path
// flowed the value into the audit LIMIT $2 parameter, where the encoding
// fails opaquely at query time. The boundary 2147483647 (MaxInt32, the
// last legal int4) parses through — the guard is a ceiling, not a
// tightening.
func TestMaintainRecallAuditLimitInt4CeilingV1241(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--recall-audit", "--limit", "2147483648")
	if code != 3 {
		t.Fatalf("exit = %d, want 3(int4 상한 사전 거부): %s", code, out)
	}
	if !strings.Contains(out, "int4 ceiling") {
		t.Fatalf("출력에 int4 상한 안내 부재: %s", out)
	}
	for _, sql := range f.queries {
		if strings.Contains(sql, "AS bad_recall") {
			t.Errorf("사전 거부 전 audit 질의 발화: %s", sql)
		}
	}

	// The boundary value parses through and the report runs (exit 0).
	f2 := &fakeMaintStore{fakeStore: &fakeStore{}}
	out2, code2 := runMaintainArgs(t, f2, "--recall-audit", "--limit", "2147483647")
	if code2 != 0 {
		t.Fatalf("경계값(2147483647) exit = %d, want 0(파싱 통과): %s", code2, out2)
	}
	if !strings.Contains(out2, `"status":"recall-audit"`) {
		t.Fatalf("경계값 보고 미생성: %s", out2)
	}
}

// TestMaintainRecallAuditRealSlow — slow (FLINT_TEST_DSN — the verify-gate
// DB): synthetic seeds on an isolated scratch DB — a topic with two
// recalled-then-invalidated rows plus one active row (the candidate), and a
// boundary topic with only one bad row (not listed). The real path lists
// exactly the candidate, stays read-only, accrues the listed counter, and
// the approval path (--invalidate --id with --reason) then accrues the
// disposed counter.
func TestMaintainRecallAuditRealSlow(t *testing.T) {
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
	name := fmt.Sprintf("flint_t4recall_%d", time.Now().UnixNano()%1e9)
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
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seed := func(hash, topic, summary string) map[string]any {
		return map[string]any{
			"kind": "knowledge", "topic": topic, "summary": summary, "tags": ":adr:",
			"confidence": 0.9, "content_hash": hash, "status": "active",
		}
	}
	topic := "t4-recall-audit 데모 배포 절차"
	border := "t4-recall-audit 회고 리듬"
	added, err := ms.InsertBatch(ctx, []map[string]any{
		seed("t4-ra-a", topic, "첫째 회상 후 무효화된 행의 요약"),
		seed("t4-ra-b", topic, "둘째 회상 후 무효화된 행의 요약"),
		seed("t4-ra-c", topic, "활성 후보 행의 요약"),
		seed("t4-ra-d", border, "경계 주제의 나쁜 행"),
		seed("t4-ra-e", border, "경계 주제의 활성 행"),
	})
	if err != nil {
		t.Fatalf("시드: %v", err)
	}
	// recall history on the bad rows, then invalidate them: topic earns
	// bad_recall 2 (>= min), the border topic only 1 (excluded).
	for _, id := range added[:2] {
		if _, err := ms.Query(ctx,
			`INSERT INTO recall_events (knowledge_id) VALUES ($1)`, id); err != nil {
			t.Fatalf("recall_events 시드: %v", err)
		}
	}
	if _, err := ms.Query(ctx,
		`INSERT INTO recall_events (knowledge_id) VALUES ($1)`, added[3]); err != nil {
		t.Fatalf("경계 recall_events 시드: %v", err)
	}
	if _, err := ms.Invalidate(ctx, added[:2], "t4 recall-audit demo invalidation"); err != nil {
		t.Fatalf("무효화: %v", err)
	}
	if _, err := ms.Invalidate(ctx, []int{added[3]}, "t4 recall-audit boundary invalidation"); err != nil {
		t.Fatalf("경계 무효화: %v", err)
	}
	var out strings.Builder
	if code := RunMaintain(ctx, ms, []string{"--recall-audit"}, nil, &out); code != 0 {
		t.Fatalf("--recall-audit exit = %d: %s", code, out.String())
	}
	var got recallAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out.String())
	}
	if got.Status != "recall-audit" || got.MinBad != 2 || got.Count != 1 || len(got.Items) != 1 {
		t.Fatalf("status/min_bad/count = %q/%d/%d — %s", got.Status, got.MinBad, got.Count, out.String())
	}
	if got.Items[0].ID != added[2] || got.Items[0].BadRecall != 2 {
		t.Errorf("후보 = %+v, want 활성 행 bad_recall 2(경계 주제 1 제외)", got.Items[0])
	}
	// listing only: the candidate stays active.
	rows, err := ms.Query(ctx,
		`SELECT invalid_at FROM knowledge WHERE id = $1`, added[2])
	if err != nil || len(rows) != 1 || rows[0][0] != nil {
		t.Fatalf("후보 활성 유지 확인: %v %v", rows, err)
	}
	// the listed counter landed in meta.
	metaRows, err := ms.Query(ctx,
		`SELECT value FROM meta WHERE key LIKE 'daily:recall_audit_listed:%'`)
	if err != nil || len(metaRows) == 0 {
		t.Fatalf("recall_audit_listed 카운터 미적립: %v %s", err, out.String())
	}
	// the approval path: disposing the qualified candidate accrues the
	// disposed counter (D4).
	var disp strings.Builder
	if code := RunMaintain(ctx, ms, []string{"--invalidate", "--id", fmt.Sprint(added[2]),
		"--reason", "t4 recall-audit demo disposition"}, nil, &disp); code != 0 {
		t.Fatalf("--invalidate exit = %d: %s", code, disp.String())
	}
	dispRows, err := ms.Query(ctx,
		`SELECT value FROM meta WHERE key LIKE 'daily:recall_audit_disposed:%'`)
	if err != nil || len(dispRows) == 0 {
		t.Fatalf("recall_audit_disposed 카운터 미적립: %v %s", err, disp.String())
	}
}

// ── v1.25.0 T1: maintain --gc (the dolt garbage-collection wrapper) ──

// gcPingSeqStore — a fakeMaintStore whose Ping fails the first failPings
// times before succeeding (the GC reconnect retry sequence — fakeStore's
// static pingErr cannot express "fails twice, then reconnects").
type gcPingSeqStore struct {
	*fakeMaintStore
	failPings int
	pings     int
}

func (f *gcPingSeqStore) Ping(ctx context.Context) error {
	if f.pings < f.failPings {
		f.pings++
		return errors.New("conn closed")
	}
	f.pings++
	return nil
}

// withGCSeams — swaps the GC execution seam and the ping-interval seam
// (restored after the test). The interval is 0 so the retry loop stays fast.
func withGCSeams(t *testing.T, gc func(ctx context.Context, st wire.Store, shallow bool) error) {
	t.Helper()
	oldFn, oldDelay := maintainGCFn, maintainGCPingDelay
	maintainGCFn, maintainGCPingDelay = gc, 0
	t.Cleanup(func() { maintainGCFn, maintainGCPingDelay = oldFn, oldDelay })
}

// gcQueryFn — answers the schema recheck and records the meta upserts.
func gcQueryFn(metaWrites map[string]string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"19"}}, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			if len(args) >= 2 {
				metaWrites[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
			}
			return [][]any{{args[0]}}, nil
		}
		return nil, nil
	}
}

// metaUpsertCount — the number of recorded meta INSERT queries (the
// "meta not recorded on failure" assertions).
func metaUpsertCount(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "INSERT INTO meta") {
			n++
		}
	}
	return n
}

// runGCMaintainArgs — runMaintainArgs is fixed to *fakeMaintStore, so the
// Ping-sequence wrapper gets a local runner (the runLimitArgs convention).
func runGCMaintainArgs(t *testing.T, f *gcPingSeqStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestMaintainGCYesGuard — --gc without --yes is exit 1 (the mode-combination
// error convention) and --shallow under a foreign mode is rejected the same way.
func TestMaintainGCYesGuard(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--gc")
	if code != 1 {
		t.Fatalf("--gc (no --yes) exit=%d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "--gc requires --yes") {
		t.Errorf("가이드 문구에 --yes 요구 없음: %s", out)
	}
	out, code = runMaintainArgs(t, f, "--check", "--shallow")
	if code != 1 {
		t.Fatalf("--check --shallow exit=%d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "--shallow is not valid with --check") {
		t.Errorf("--shallow 소유권 오류 문구 없음: %s", out)
	}
}

// TestMaintainGCRunContract — the happy path: the disconnect notice line,
// --gc execution via the seam (full mode), schema recheck, the meta pair,
// and the JSON result exit 0.
func TestMaintainGCRunContract(t *testing.T) {
	metaWrites := map[string]string{}
	f := &gcPingSeqStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{
		queryFn: gcQueryFn(metaWrites),
	}}}
	gcCalled, gcShallow := false, false
	withGCSeams(t, func(ctx context.Context, st wire.Store, shallow bool) error {
		gcCalled, gcShallow = true, shallow
		return nil
	})
	out, code := runGCMaintainArgs(t, f, "--gc", "--yes")
	if code != 0 {
		t.Fatalf("--gc --yes exit=%d, want 0: %s", code, out)
	}
	if !gcCalled || gcShallow {
		t.Errorf("GC seam 호출=%v shallow=%v, want true/false", gcCalled, gcShallow)
	}
	if !strings.Contains(out, "disconnect") {
		t.Errorf("사전 안내에 접속 단절 명시 없음: %s", out)
	}
	if !strings.Contains(out, `"status":"gc"`) || !strings.Contains(out, `"mode":"full"`) {
		t.Errorf("결과 JSON status/mode 불일치: %s", out)
	}
	if v, ok := metaWrites["last_gc_at"]; !ok || v == "" {
		t.Errorf("last_gc_at 미기록: %v", metaWrites)
	}
	if v := metaWrites["last_gc_mode"]; v != "full" {
		t.Errorf("last_gc_mode=%q, want full", v)
	}
	if f.pings != 1 {
		t.Errorf("Ping 호출 %d회, want 1(성공 시 즉시 종료)", f.pings)
	}
}

// TestMaintainGCShallowMode — --shallow rides the seam flag and lands in
// last_gc_mode as "shallow".
func TestMaintainGCShallowMode(t *testing.T) {
	metaWrites := map[string]string{}
	f := &gcPingSeqStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{
		queryFn: gcQueryFn(metaWrites),
	}}}
	var gcShallow bool
	withGCSeams(t, func(ctx context.Context, st wire.Store, shallow bool) error {
		gcShallow = shallow
		return nil
	})
	out, code := runGCMaintainArgs(t, f, "--gc", "--shallow", "--yes")
	if code != 0 {
		t.Fatalf("--gc --shallow --yes exit=%d, want 0: %s", code, out)
	}
	if !gcShallow {
		t.Error("--shallow가 seam에 전달되지 않음")
	}
	if v := metaWrites["last_gc_mode"]; v != "shallow" {
		t.Errorf("last_gc_mode=%q, want shallow", v)
	}
}

// TestMaintainGCReconnectRetryCap — a Ping that fails twice then succeeds
// recovers within the 5-attempt cap (3 pings total).
func TestMaintainGCReconnectRetryCap(t *testing.T) {
	f := &gcPingSeqStore{failPings: 2,
		fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{
			queryFn: gcQueryFn(map[string]string{}),
		}}}
	withGCSeams(t, func(ctx context.Context, st wire.Store, shallow bool) error { return nil })
	out, code := runGCMaintainArgs(t, f, "--gc", "--yes")
	if code != 0 {
		t.Fatalf("재시도 후 exit=%d, want 0: %s", code, out)
	}
	if f.pings != 3 {
		t.Errorf("Ping 호출 %d회, want 3(2 실패 후 성공)", f.pings)
	}
}

// TestMaintainGCReconnectFailed — 5 ping failures end exit 1 with the
// "reconnect failed" JSON, and NO meta write happens (the GC ran but the
// completion is unverified).
func TestMaintainGCReconnectFailed(t *testing.T) {
	f := &gcPingSeqStore{failPings: 100,
		fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{
			queryFn: gcQueryFn(map[string]string{}),
		}}}
	gcCalled := false
	withGCSeams(t, func(ctx context.Context, st wire.Store, shallow bool) error {
		gcCalled = true
		return nil
	})
	out, code := runGCMaintainArgs(t, f, "--gc", "--yes")
	if code != 1 {
		t.Fatalf("Ping 전량 실패 exit=%d, want 1: %s", code, out)
	}
	if !gcCalled {
		t.Error("GC seam이 실행되지 않음")
	}
	if !strings.Contains(out, "reconnect failed") {
		t.Errorf("reconnect failed 문구 없음: %s", out)
	}
	if n := metaUpsertCount(f.fakeStore); n != 0 {
		t.Errorf("실패 시 meta 기록 %d건, want 0", n)
	}
	if f.pings != 5 {
		t.Errorf("Ping 호출 %d회, want 5(상한)", f.pings)
	}
}

// TestMaintainGCSchemaRecheckGate — a successful reconnect followed by a
// schema_version lookup failure is exit 1 with no meta write (the schema
// gate precedes the record).
func TestMaintainGCSchemaRecheckGate(t *testing.T) {
	f := &gcPingSeqStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{
		queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "schema_version") {
				return nil, errors.New("schema lookup failed")
			}
			return nil, nil
		},
	}}}
	withGCSeams(t, func(ctx context.Context, st wire.Store, shallow bool) error { return nil })
	out, code := runGCMaintainArgs(t, f, "--gc", "--yes")
	if code != 1 {
		t.Fatalf("schema 재검증 실패 exit=%d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "schema recheck failed") {
		t.Errorf("schema 재검증 오류 문구 없음: %s", out)
	}
	if n := metaUpsertCount(f.fakeStore); n != 0 {
		t.Errorf("게이트 실패 시 meta 기록 %d건, want 0", n)
	}
}

// TestMetaKnownKeysGC — the GC history pair joins the known meta keys (no
// meta-audit unknown false positive — the R80-1 self-contradiction class).
func TestMetaKnownKeysGC(t *testing.T) {
	if !KnownMetaKey("last_gc_at") || !KnownMetaKey("last_gc_mode") {
		t.Fatal("last_gc_at·last_gc_mode가 known 키에 없음")
	}
}
