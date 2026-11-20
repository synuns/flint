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
