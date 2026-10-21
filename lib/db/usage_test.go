// usage_test.go — T37 플레이북 사용 로그·성공률(실DB slow 테스트).
package db

import (
	"context"
	"testing"
)

// cleanPBKeys — pb_% meta 키를 지워 테스트끼리 격리한다.
// LIKE의 '_'는 임의 문자이므로 백슬래시 이스케이프로 리터럴 취급.
func cleanPBKeys(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key LIKE 'pb\_%'`); err != nil {
		t.Fatalf("clean pb keys: %v", err)
	}
}

// TestPlaybookStats — 3회 중 2회 성공 → Uses 3·OK 2·Rate 0.667(플랜 T37).
func TestPlaybookStats(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanPBKeys(t, r)
	for _, ok := range []bool{true, false, true} {
		if err := r.LogPlaybookUse(ctx, 42, ok); err != nil {
			t.Fatalf("LogPlaybookUse(ok=%v): %v", ok, err)
		}
	}
	// meta 키 직접 검증 — 증분이 정확히 3/2(비멱등은 의도적 로그 계약).
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

// TestPlaybookStatsMultiple — 플레이북 2건이 id 오름차순으로, 성공만 ok 증가.
func TestPlaybookStatsMultiple(t *testing.T) {
	r := newTestRepo(t)
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
	// id 오름차순 정렬 계약.
	if stats[0].ID != 3 || stats[0].Uses != 1 || stats[0].OK != 0 || stats[0].Rate != 0 {
		t.Fatalf("stats[0] = %+v, want {3 1 0 0}", stats[0])
	}
	if stats[1].ID != 7 || stats[1].Uses != 2 || stats[1].OK != 2 || stats[1].Rate != 1 {
		t.Fatalf("stats[1] = %+v, want {7 2 2 1}", stats[1])
	}
}

// TestPlaybookStatsEmpty — 로그 없으면 빈 슬라이스·nil 에러.
func TestPlaybookStatsEmpty(t *testing.T) {
	r := newTestRepo(t)
	cleanPBKeys(t, r)
	stats, err := r.PlaybookStats(context.Background())
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats = %v, want empty", stats)
	}
}
