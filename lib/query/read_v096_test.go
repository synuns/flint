package query

import (
	"reflect"
	"testing"
)

func TestTokenizeQuerySplits(t *testing.T) {
	if got := tokenizeQuery("flint 백업 복구 절차"); !reflect.DeepEqual(got, []string{"flint", "백업", "복구", "절차"}) {
		t.Errorf("tokenizeQuery = %v", got)
	}
}

func TestTokenizeQueryDropsPunct(t *testing.T) {
	if got := tokenizeQuery("read, write!"); !reflect.DeepEqual(got, []string{"read", "write"}) {
		t.Errorf("구두점 미제거: %v", got)
	}
}

func TestFallbackMatchCount(t *testing.T) {
	if n := fallbackMatchCount([]string{"백업", "복구"}, "flint 백업", "복구는 저장소 정지 조건에서 확인이다"); n != 2 {
		t.Errorf("match count = %d, want 2", n)
	}
	if n := fallbackMatchCount([]string{"없는말"}, "flint 백업", "요약"); n != 0 {
		t.Errorf("무매치 = %d, want 0", n)
	}
}
