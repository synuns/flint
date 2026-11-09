// rank_v0130_test.go — v0.13.0 R1 채택 재랭킹: read_count 최종 타이브레이크.
package query

import (
	"strings"
	"testing"
)

func TestRankOrderEndsWithReadCount(t *testing.T) {
	got := RankOrder(2) // v0.28.0: 파라미터 인덱스
	if !strings.Contains(got, "last_read_at DESC NULLS LAST, read_count DESC") {
		t.Errorf("read_count 타이브레이커 누락: %s", got)
	}
}

// TestFallbackReorderByAdoption — 폴백 재랭킹 2차 타이브레이크: 정합수
// 내림 차순 후 read_count DESC. 동률(정합수·read_count 모두)은 기존
// 조회 순서를 보존한다.
func TestFallbackReorderByAdoption(t *testing.T) {
	// v0.25.0 재작성: 순차 타이브레이크(정합>read)에서 RRF 융합으로
	// 의미론 변경 — 다중 신호 기여 행이 단일 신호 우위 행을 이긴다.
	// 원 의도(채택 신호가 순위에 반영·동률은 기존 순서)는 유지.
	rows := [][]any{
		{int32(1), "flint 백업", "복구 절차", ":a:", nil, "main", 0.9, "active", int32(0)}, // match2·rc0·jamo2
		{int32(2), "flint 백업", "복구 절차", ":a:", nil, "main", 0.8, "active", int32(5)}, // match2·rc5·jamo2
		{int32(3), "복구", "요약", ":a:", nil, "main", 0.7, "active", int32(2)},          // match1·rc2·jamo1
		{int32(4), "복구", "요약", ":a:", nil, "main", 0.6, "active", int32(2)},          // 동률 — 기존 3→4 순서
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("3신호 전부 상위 행이 첫 자리 아님: %v", rows[0][0])
	}
	// 3·4(3신호 소액 기여)는 1(2신호)보다 위 — RRF 다중 기여 속성.
	if rows[1][0] != int32(3) || rows[2][0] != int32(4) {
		t.Errorf("다중 신호 행 우선 위반: %v then %v", rows[1][0], rows[2][0])
	}
	if rows[3][0] != int32(1) {
		t.Errorf("단일 신호 최하: %v", rows[3][0])
	}
}
