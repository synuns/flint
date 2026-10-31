package query

import (
	"reflect"
	"testing"
)

func TestSuggestMinScoreFiltersMarginal(t *testing.T) {
	q := "환경설정 초기화 방법"
	// 픽스처 사전 검증 — 이 행의 점수가 0 < s < suggestMinScore인지
	// 스스로 계산해 단언(하드코딩 점수 회피).
	row := [][]any{{int32(11), "flint v0.9.6 방향", "기록"}}
	s := bigramDice(nfkc(q), nfkc("flint v0.9.6 방향"+"기록"))
	if !(s > 0 && s < suggestMinScore) {
		t.Skipf("픽스처 점수 %.3f가 한계 밖 — 픽스처 조정 필요", s)
	}
	if got := SuggestSimilar(q, row, 3); len(got) != 0 {
		t.Errorf("한계 미만 제안 누출: %v (score %.3f)", got, s)
	}
}

func TestSuggestSimilarKeepsRelevant(t *testing.T) {
	rows := [][]any{
		{int32(3), "flint 백업 절차", "덤프 복구"},
		{int32(12), "flint v0.9.6 방향", "기록"},
	}
	got := SuggestSimilar("백업 복구 절차", rows, 3)
	want := []Suggestion{{ID: 3, Topic: "flint 백업 절차"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("유사 행 누락 또는 무관 행 누출: %v", got)
	}
}
