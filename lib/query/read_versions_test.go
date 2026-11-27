package query

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildReadStatusSQL(t *testing.T) {
	sql, args := BuildReadStatus("", Filters{Status: "pending", Limit: 20})
	if !strings.Contains(sql, "FROM knowledge") || strings.Contains(sql, "active_knowledge") {
		t.Errorf("base 테이블 미사용: %s", sql)
	}
	if !strings.Contains(sql, "status = $") {
		t.Errorf("status 조건 누락: %s", sql)
	}
	if len(args) == 0 || args[0] != "pending" {
		t.Errorf("파라미터 = %v", args)
	}
}

func TestBuildReadFallbackIncludesTags(t *testing.T) {
	sql, _, ok := BuildReadFallback("doltgres 포트", Filters{})
	if !ok {
		t.Fatal("ok=false, want 폴백 조립(토큰 2)")
	}
	if !strings.Contains(sql, "topic || ' ' || summary || ' ' || tags") {
		t.Fatalf("폴백 접합면에 tags 누락: %s", sql)
	}
}

func TestBuildReadPhraseSurfaceUnchanged(t *testing.T) {
	sql, _ := BuildRead("doltgres", Filters{})
	if !strings.Contains(sql, "lower(topic || ' ' || summary) LIKE") {
		t.Fatalf("구문 접합면 변형: %s", sql)
	}
	if strings.Contains(sql, "|| ' ' || tags") {
		t.Fatalf("구문 쿼리에 tags 유입 금지: %s", sql)
	}
}

func TestRankOrderTieBreak(t *testing.T) {
	cases := []struct {
		name        string
		placeholder int
	}{
		{"통상 파라미터", 3},
		{"첫 파라미터", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RankOrder(tc.placeholder)

			if !strings.HasSuffix(got, "last_read_at DESC NULLS LAST, read_count DESC") {
				t.Fatalf("RankOrder 접미 불일치: %q", got)
			}

			wantPrefix := fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC", tc.placeholder)
			if !strings.HasPrefix(got, wantPrefix) {
				t.Fatalf("RankOrder 접두 불변 위반:\n got  %q\n want %q", got, wantPrefix)
			}
		})
	}
}

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

func TestTokenizeQueryCapsAtEight(t *testing.T) {
	got := tokenizeQuery("a b c d e f g h i j")
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokenizeQuery 상한 미적용: %v", got)
	}
}

func TestBuildReadFallbackSkipsSingleToken(t *testing.T) {
	if _, _, ok := BuildReadFallback("백업", queryDefaultFilters()); ok {
		t.Errorf("1-토큰 질의에 폴백 조립됨")
	}
	if _, _, ok := BuildReadFallback("", queryDefaultFilters()); ok {
		t.Errorf("빈 질의에 폴백 조립됨")
	}
}

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
	if !strings.Contains(sql, " OR ") || !strings.Contains(sql, "FROM recall_knowledge") {
		t.Errorf("폴백 SQL 형태 오류: %q", sql)
	}

	if last, ok := args[len(args)-1].(int); !ok || last != 50 {
		t.Errorf("limit 인자 = %v, want 50", args[len(args)-1])
	}
}

func TestReorderByFallbackCountStableTies(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업", "요약1", ":a:", nil, "main", 0.9, "active"},
		{int32(2), "flint 백업", "복구는 저장소 정지", ":a:", nil, "main", 0.8, "active"},
		{int32(3), "복구", "요약3", ":a:", nil, "main", 0.7, "active"},
	}
	ReorderByFallbackCount(rows, []string{"백업", "복구"})
	if rows[0][0] != int32(2) {
		t.Errorf("정합수 2행이 첫 자리 아님: %v", rows[0][0])
	}
	if rows[1][0] != int32(1) || rows[2][0] != int32(3) {
		t.Errorf("동률 순서 깨짐(기존 조회 순 보존 필요): %v then %v", rows[1][0], rows[2][0])
	}
}

func queryDefaultFilters() Filters { return Filters{} }

func TestFormatVaultRowsPipeStrippedAndTitleTruncated(t *testing.T) {
	title := strings.Repeat("제", 250)
	rows := [][]any{{title, "a|b", "", "no|te.md"}}

	txt := FormatVaultRows(rows, false)
	line := strings.TrimRight(txt, "\n")
	parts := strings.Split(line, "|")
	if len(parts) != 4 {
		t.Fatalf("텍스트 라인 필드 수=%d, want 4 (파이프 이스케이프): %q", len(parts), line)
	}
	if parts[0] != "vault" {
		t.Fatalf("접두=%q, want vault", parts[0])
	}
	if parts[1] != "no te.md" {
		t.Fatalf("path 셀=%q, want 'no te.md' (| 제거)", parts[1])
	}
	if got := utf8.RuneCountInString(parts[2]); got != 200 {
		t.Fatalf("title 셀 rune 수=%d, want 200", got)
	}
	if parts[3] != "a b" {
		t.Fatalf("summary 셀=%q, want 'a b' (| 제거)", parts[3])
	}

	j := FormatVaultRows(rows, true)
	var v VaultLine
	if err := json.Unmarshal([]byte(strings.TrimRight(j, "\n")), &v); err != nil {
		t.Fatalf("json 파싱: %v — %q", err, j)
	}
	if v.Path != "no|te.md" || v.Summary != "a|b" {
		t.Fatalf("json 모드 셀 변형 금지: %+v", v)
	}
	if got := utf8.RuneCountInString(v.Title); got != 200 {
		t.Fatalf("json title rune 수=%d, want 200", got)
	}
}

func recallSignalRow(id int64, topic, summary string, readCount int32) []any {
	return []any{id, topic, summary, "", "", "", "decision", 0.9, "", "active", readCount}
}

func TestReadRecallSignalOrdersRows(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic note", 2),
	}
	SetRecallCounts(map[int]int{2: 3})
	ReorderByFallbackCount(rows, TokenizeQuery("alpha"))
	if got := whyRowID(rows[0]); got != "2" {
		t.Fatalf("recall 3건 행이 상위여야 함: 상위 id=%s", got)
	}
	if got := whyRowID(rows[1]); got != "1" {
		t.Fatalf("recall 0건 행이 하위여야 함: 하위 id=%s", got)
	}
}

func TestReadNoSignalKeepsOrder(t *testing.T) {
	rows := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	ReorderByFallbackCount(rows, TokenizeQuery("alpha policy"))
	if got := whyRowID(rows[0]); got != "1" {
		t.Fatalf("신호 부재 시 기존 순위 유지: 상위 id=%s", got)
	}

	defer SetRecallCounts(nil)
	SetRecallCounts(map[int]int{99: 5})
	rows2 := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	ReorderByFallbackCount(rows2, TokenizeQuery("alpha policy"))
	if got := whyRowID(rows2[0]); got != "1" {
		t.Fatalf("미등록 id 행은 기존 순위 유지: 상위 id=%s", got)
	}
}

func TestReadRecallSignalWhyEvidence(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha demo", "alpha synthetic note", 2),
	}
	tokens := TokenizeQuery("alpha")

	SetRecallCounts(map[int]int{1: 3})
	ev := RRFWhyEvidence(rows, tokens)
	if len(ev) != 1 || len(ev[0].RRF) != 5 {
		t.Fatalf("recall 신호 편입 시 RRF 신호 수 = %d, want 5", len(ev[0].RRF))
	}
	last := ev[0].RRF[len(ev[0].RRF)-1]
	if last.Name != "recall" || last.Value != 3 || last.Rank != 1 {
		t.Fatalf("recall 신호 evidence = %+v, want Name=recall Value=3 Rank=1", last)
	}

	SetRecallCounts(nil)
	ev = RRFWhyEvidence(rows, tokens)
	if len(ev) != 1 || len(ev[0].RRF) != 4 {
		t.Fatalf("신호 부재 시 RRF 신호 수 = %d, want 4", len(ev[0].RRF))
	}
	for _, s := range ev[0].RRF {
		if s.Name == "recall" {
			t.Fatalf("신호 부재 시 recall 신호 노출: %+v", s)
		}
	}
}

func TestReadRecallSignalPreservesExistingSignalOrder(t *testing.T) {
	defer SetRecallCounts(nil)
	rows := [][]any{
		recallSignalRow(1, "alpha policy demo", "alpha synthetic note", 2),
		recallSignalRow(2, "alpha demo", "alpha synthetic method", 2),
	}
	SetRecallCounts(map[int]int{1: 3, 2: 3})
	ReorderByFallbackCount(rows, TokenizeQuery("alpha policy"))
	if got := fmt.Sprint(whyRowID(rows[0]), whyRowID(rows[1])); got != "12" {
		t.Fatalf("recall 동등 시 기존 순위 유지: 순서 = %s, want 12", got)
	}
}
