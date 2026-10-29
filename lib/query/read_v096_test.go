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

// TestTokenizeQueryCapsAtEight — 토큰 상한 8(스캔 비용 가드): 9개 이상
// 입력은 처음 8개만.
func TestTokenizeQueryCapsAtEight(t *testing.T) {
	got := tokenizeQuery("a b c d e f g h i j")
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokenizeQuery 상한 미적용: %v", got)
	}
}

// TestBuildReadFallbackSkipsSingleToken — 1-토큰 질의는 구문 매칭과
// 동질이라 폴백 생략(ok=false).
func TestBuildReadFallbackSkipsSingleToken(t *testing.T) {
	if _, _, ok := BuildReadFallback("백업", queryDefaultFilters()); ok {
		t.Errorf("1-토큰 질의에 폴백 조립됨")
	}
	if _, _, ok := BuildReadFallback("", queryDefaultFilters()); ok {
		t.Errorf("빈 질의에 폴백 조립됨")
	}
}

// TestBuildReadFallbackEscapesLike — 토큰의 % _ \ 는 리터럴로 이스케이프
// 되고 필터·limit 인자가 뒤따른다.
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
	// 마지막 인자는 ClampLimit 상한(99 → 50).
	if last, ok := args[len(args)-1].(int); !ok || last != 50 {
		t.Errorf("limit 인자 = %v, want 50", args[len(args)-1])
	}
}

// TestReorderByFallbackCountStableTies — 동률은 조회 순(RankOrder 순)을
// 유지한다(SliceStable 계약).
func TestReorderByFallbackCountStableTies(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업", "요약1", ":a:", nil, "main", 0.9, "active"}, // 정합수 1
		{int32(2), "flint 백업", "복구는 저장소 정지", ":a:", nil, "main", 0.8, "active"}, // 정합수 2
		{int32(3), "복구", "요약3", ":a:", nil, "main", 0.7, "active"}, // 정합수 1 (동률 — 기존 1→3 순서)
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("정합수 2행이 첫 자리 아님: %v", rows[0][0])
	}
	if rows[1][0] != int32(1) || rows[2][0] != int32(3) {
		t.Errorf("동률 순서 깨짐(기존 조회 순 보존 필요): %v then %v", rows[1][0], rows[2][0])
	}
}

// queryDefaultFilters — 기본 Filters(테스트 국소).
func queryDefaultFilters() Filters { return Filters{} }
