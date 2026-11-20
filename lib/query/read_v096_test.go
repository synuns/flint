package query

import (
	"reflect"
	"strings"
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

// TestTokenizeQueryCapsAtEight — the token cap of 8 (a scan-cost guard): inputs of
// 9+ tokens keep only the first 8.
func TestTokenizeQueryCapsAtEight(t *testing.T) {
	got := tokenizeQuery("a b c d e f g h i j")
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokenizeQuery 상한 미적용: %v", got)
	}
}

// TestBuildReadFallbackSkipsSingleToken — a 1-token query is identical to phrase
// matching, so the fallback is skipped (ok=false).
func TestBuildReadFallbackSkipsSingleToken(t *testing.T) {
	if _, _, ok := BuildReadFallback("백업", queryDefaultFilters()); ok {
		t.Errorf("1-토큰 질의에 폴백 조립됨")
	}
	if _, _, ok := BuildReadFallback("", queryDefaultFilters()); ok {
		t.Errorf("빈 질의에 폴백 조립됨")
	}
}

// TestBuildReadFallbackEscapesLike — the tokens' %, _, and \\ are escaped as
// literals, and the filter and limit arguments follow.
func TestBuildReadFallbackEscapesLike(t *testing.T) {
	sql, args, ok := BuildReadFallback("a%b c_d", Filters{Tag: "t", Limit: 99})
	if !ok {
		t.Fatalf("폴백 조립 실패")
	}
	if got := args[0].(string); got != `%a\%b%` {
		t.Errorf("첫 토큰 이스케이프 = %q", got)
	}
	if got := args[1].(string); got != `%c\_d%` {
		t.Errorf("둘째 토큰 이스케이프 = %q", got)
	}
	if !strings.Contains(sql, " OR ") || !strings.Contains(sql, "FROM active_knowledge") {
		t.Errorf("폴백 SQL 형태 오류: %q", sql)
	}
	// the last argument is the ClampLimit cap (99 -> 50).
	if last, ok := args[len(args)-1].(int); !ok || last != 50 {
		t.Errorf("limit 인자 = %v, want 50", args[len(args)-1])
	}
}

// TestReorderByFallbackCountStableTies — ties keep the query order (the
// RankOrder order) (the SliceStable contract).
func TestReorderByFallbackCountStableTies(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업", "요약1", ":a:", nil, "main", 0.9, "active"},              // 1 exact match
		{int32(2), "flint 백업", "복구는 저장소 정지", ":a:", nil, "main", 0.8, "active"}, // 2 exact matches
		{int32(3), "복구", "요약3", ":a:", nil, "main", 0.7, "active"},              // 1 exact match (tie — existing 1->3 order)
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("정합수 2행이 첫 자리 아님: %v", rows[0][0])
	}
	if rows[1][0] != int32(1) || rows[2][0] != int32(3) {
		t.Errorf("동률 순서 깨짐(기존 조회 순 보존 필요): %v then %v", rows[1][0], rows[2][0])
	}
}

// queryDefaultFilters — the default Filters (test-local).
func queryDefaultFilters() Filters { return Filters{} }
