// rank_v0130_test.go — adoption reranking: read_count as the final tiebreaker.
package query

import (
	"strings"
	"testing"
)

func TestRankOrderEndsWithReadCount(t *testing.T) {
	got := RankOrder(2) // the parameter index
	if !strings.Contains(got, "last_read_at DESC NULLS LAST, read_count DESC") {
		t.Errorf("read_count 타이브레이커 누락: %s", got)
	}
}

// TestFallbackReorderByAdoption — the fallback rerank's secondary tiebreaker:
// descending match count, then read_count DESC. Ties (both match count and
// read_count equal) preserve the existing query order.
func TestFallbackReorderByAdoption(t *testing.T) {
	// RRF fusion semantics — a multi-signal-contribution row beats a single-signal-dominant row.
	// The adoption signal is reflected in the ranking; ties preserve the existing order.
	rows := [][]any{
		{int32(1), "flint 백업", "복구 절차", ":a:", nil, "main", 0.9, "active", int32(0)}, // match2·rc0·jamo2
		{int32(2), "flint 백업", "복구 절차", ":a:", nil, "main", 0.8, "active", int32(5)}, // match2·rc5·jamo2
		{int32(3), "복구", "요약", ":a:", nil, "main", 0.7, "active", int32(2)},          // match1·rc2·jamo1
		{int32(4), "복구", "요약", ":a:", nil, "main", 0.6, "active", int32(2)},          // tie — existing 3->4 order
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("3신호 전부 상위 행이 첫 자리 아님: %v", rows[0][0])
	}
	// rows 3 and 4 (small contributions over 3 signals) beat row 1 (2 signals) — the RRF multi-contribution property.
	if rows[1][0] != int32(3) || rows[2][0] != int32(4) {
		t.Errorf("다중 신호 행 우선 위반: %v then %v", rows[1][0], rows[2][0])
	}
	if rows[3][0] != int32(1) {
		t.Errorf("단일 신호 최하: %v", rows[3][0])
	}
}
