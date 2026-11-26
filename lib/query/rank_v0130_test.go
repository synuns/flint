package query

import (
	"strings"
	"testing"
)

func TestRankOrderEndsWithReadCount(t *testing.T) {
	got := RankOrder(2)
	if !strings.Contains(got, "last_read_at DESC NULLS LAST, read_count DESC") {
		t.Errorf("read_count 타이브레이커 누락: %s", got)
	}
}

func TestFallbackReorderByAdoption(t *testing.T) {

	rows := [][]any{
		{int32(1), "flint 백업", "복구 절차", ":a:", nil, "main", "decision", 0.9, "", "active", int32(0)},
		{int32(2), "flint 백업", "복구 절차", ":a:", nil, "main", "decision", 0.8, "", "active", int32(5)},
		{int32(3), "복구", "요약", ":a:", nil, "main", "procedure", 0.7, "", "active", int32(2)},
		{int32(4), "복구", "요약", ":a:", nil, "main", "lesson", 0.6, "", "active", int32(2)},
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("3신호 전부 상위 행이 첫 자리 아님: %v", rows[0][0])
	}

	if rows[1][0] != int32(3) || rows[2][0] != int32(4) {
		t.Errorf("다중 신호 행 우선 위반: %v then %v", rows[1][0], rows[2][0])
	}
	if rows[3][0] != int32(1) {
		t.Errorf("단일 신호 최하: %v", rows[3][0])
	}
}
