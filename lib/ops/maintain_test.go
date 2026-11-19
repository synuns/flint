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
// v1.9.0 derived switch, the 3 columns id, cnt (event count), and last_at (last recall
// time)). Also verifies that never-recalled (NULL — reproducing the Go defense for the
// server-side WHERE exclusion) and 0-day-elapsed rows are excluded by the candidate filter.
func decayAuditRowsFixture(now time.Time) [][]any {
	return [][]any{
		{int32(3), int32(5), now.Add(-100 * 24 * time.Hour)}, // fully decayed (floor 0)
		{int32(1), int32(5), now.Add(-25 * time.Hour)},       // 1-day decay
		{int32(2), int32(7), nil},                            // never recalled — excluded
		{int32(4), int32(9), now.Add(-1 * time.Hour)},        // 0 days — excluded
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
				{int32(1), int32(5), now.Add(-6 * 24 * time.Hour)},  // 5-0.5*6=2 → archive
				{int32(2), int32(10), now.Add(-4 * 24 * time.Hour)}, // 10-0.5*4=8 -> kept
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
