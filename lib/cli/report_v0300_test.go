package cli

import (
	"testing"
	"time"
)

func TestFirstExactRank(t *testing.T) {
	rows := [][]any{
		{int32(1), "다른 주제", "요약"},
		{int32(2), "검색 품질", "요약"},
		{int32(3), "검색 품질 추가", "요약"},
	}
	if got := firstExactRank(rows, "검색 품질"); got != 2 {
		t.Errorf("firstExactRank = %d, want 2(첫 정합 행)", got)
	}
	if got := firstExactRank(rows, "없는 주제"); got != 0 {
		t.Errorf("정합 없음 = %d, want 0", got)
	}
	if got := firstExactRank(rows, ""); got != 0 {
		t.Errorf("빈 쿼리 = %d, want 0", got)
	}

	mixed := [][]any{{int32(1), "Flint 배포"}}
	if got := firstExactRank(mixed, "flint 배포"); got != 1 {
		t.Errorf("대소문자 = %d, want 1", got)
	}
}

func TestTrendOfDiff(t *testing.T) {
	now := time.Now()
	mk := func(daysAgo int, zero, fallback bool, rank int) readEventRow {
		return readEventRow{Ts: now.Add(-time.Duration(daysAgo) * 24 * time.Hour),
			Zero: zero, Fallback: fallback, FirstRank: rank}
	}

	evs := []readEventRow{
		mk(1, false, false, 1), mk(2, false, false, 1), mk(3, false, true, 4), mk(5, true, false, 0),
		mk(8, false, true, 0), mk(10, true, false, 0),
	}
	cur, prev := trendWindow(now, evs)
	if cur.N != 4 || prev.N != 2 {
		t.Fatalf("창 크기 cur=%d prev=%d, want 4/2", cur.N, prev.N)
	}
	if cur.HitPct != 75 || cur.FallbackPct != 25 || cur.Top1Pct != 66 {
		t.Errorf("cur = %+v, want hit75/fb25/top1 66(버림 정수)", cur)
	}
	tr := cur.Diff(prev)
	if tr == nil {
		t.Fatal("추세 nil — 양창 표본 있는데 nil")
	}
	if tr.HitPct.Delta != 25 {
		t.Errorf("hit delta = %d, want 25", tr.HitPct.Delta)
	}

	emptyCur, _ := trendWindow(now, nil)
	if emptyCur.Diff(prev) != nil {
		t.Error("빈 창 추세가 nil 아님")
	}
}
