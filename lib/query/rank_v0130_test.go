// rank_v0130_test.go — v0.13.0 R1 채택 재랭킹: read_count 최종 타이브레이크.
package query

import (
	"strings"
	"testing"
)

func TestRankOrderEndsWithReadCount(t *testing.T) {
	got := RankOrder("백업")
	if !strings.Contains(got, "last_read_at DESC NULLS LAST, read_count DESC") {
		t.Errorf("read_count 타이브레이크 누락: %s", got)
	}
}

// TestFallbackReorderByAdoption — 폴백 재랭킹 2차 타이브레이크: 정합수
// 내림 차순 후 read_count DESC. 동률(정합수·read_count 모두)은 기존
// 조회 순서를 보존한다.
func TestFallbackReorderByAdoption(t *testing.T) {
	rows := [][]any{
		{int32(1), "flint 백업", "복구 절차", ":a:", nil, "main", 0.9, "active", int32(0)}, // 정합 2·rc 0
		{int32(2), "flint 백업", "복구 절차", ":a:", nil, "main", 0.8, "active", int32(5)}, // 정합 2·rc 5
		{int32(3), "복구", "요약", ":a:", nil, "main", 0.7, "active", int32(2)},          // 정합 1·rc 2
		{int32(4), "복구", "요약", ":a:", nil, "main", 0.6, "active", int32(2)},          // 정합 1·rc 2 (동률 — 기존 3→4 순서)
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("정합 2·read_count 5행이 첫 자리 아님: %v", rows[0][0])
	}
	if rows[1][0] != int32(1) {
		t.Errorf("정합 동률에서 read_count 0행이 둘째 자리 아님: %v", rows[1][0])
	}
	if rows[2][0] != int32(3) || rows[3][0] != int32(4) {
		t.Errorf("read_count 동률 순서 깨짐(기존 조회 순 보존 필요): %v then %v", rows[2][0], rows[3][0])
	}
}
