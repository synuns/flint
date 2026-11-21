package query

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// TestClampLimit — limit defaults to 10, capped at 50.
func TestClampLimit(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 10},
		{-1, 10},
		{999, 50},
		{51, 50},
		{10, 10},
		{25, 25},
		{50, 50},
	}
	for _, c := range cases {
		if got := ClampLimit(c.in); got != c.want {
			t.Errorf("ClampLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestBuildReadUsesActiveKnowledge — reads are forced onto the active_knowledge view.
func TestBuildReadUsesActiveKnowledge(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	if !strings.Contains(sql, "active_knowledge") {
		t.Errorf("BuildRead SQL이 active_knowledge 뷰를 사용해야 함: %s", sql)
	}
}

// TestBuildReadEscapesLike — q's % and _ are not interpreted as LIKE wildcards
// (the WHERE clause uses placeholder injection). The ranking-clause copy is also
// parameterized ($2) — the escaped inline form itself is gone (removing the injection surface).
func TestBuildReadEscapesLike(t *testing.T) {
	sql, args := BuildRead("100%_drop'", Filters{})
	if !strings.Contains(sql, "LIKE lower($1)") {
		t.Errorf("LIKE lower($1) 패턴 누락: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY (lower(topic)=lower($2)) DESC") {
		t.Errorf("랭킹 절 파라미터 누락: %s", sql)
	}
	if len(args) < 2 || args[1] != "100%_drop'" {
		t.Fatalf("랭킹 파라미터 미전달: %v", args)
	}
	pat, ok := args[0].(string)
	if !ok {
		t.Fatalf("args[0]가 문자열이 아님: %T", args[0])
	}
	want := `%100\%\_drop'%`
	if pat != want {
		t.Errorf("이스케이프 패턴 = %q, want %q", pat, want)
	}
}

// TestBuildReadFilters — the tag/entity/domain filters are assembled as placeholders.
func TestBuildReadFilters(t *testing.T) {
	f := Filters{Tag: "a", Entity: "user", Domain: "game"}
	sql, args := BuildRead("", f)
	for _, frag := range []string{"tags", "entity = $", "domain = $"} {
		if !strings.Contains(sql, frag) {
			t.Errorf("필터 조각 %q 누락: %s", frag, sql)
		}
	}
	found := map[string]bool{"a": false, "user": false, "game": false}
	for _, a := range args {
		if s, ok := a.(string); ok {
			found[s] = true
		}
	}
	for k, v := range found {
		if !v {
			t.Errorf("필터 값 %q이 args에 없음: %v", k, args)
		}
	}
}

// TestBuildReadLimitParam — the limit's ClampLimit result is reflected in the SQL.
func TestBuildReadLimitParam(t *testing.T) {
	sql, args := BuildRead("", Filters{Limit: 999})
	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("LIMIT 절 누락: %s", sql)
	}
	last := args[len(args)-1]
	if lim, ok := last.(int); !ok || lim != 50 {
		t.Errorf("LIMIT 인자 = %v, want 50", last)
	}
}

// TestFormatRowsTruncates — the summary is truncated to 80 runes (Korean-safe).
func TestFormatRowsTruncates(t *testing.T) {
	rows := [][]any{{3, "topic", strings.Repeat("가", 100)}}
	out := FormatRows(rows, "id,topic,summary", false)
	cols := strings.Split(strings.TrimRight(out, "\n"), "|")
	if len(cols) != 3 {
		t.Fatalf("3컬럼 기대, got %d: %q", len(cols), out)
	}
	if got := len([]rune(cols[2])); got != 80 {
		t.Errorf("summary 절단 길이 = %d, want 80", got)
	}
}

// TestFormatRowsShortSummary — under 80 characters passes through as-is.
func TestFormatRowsShortSummary(t *testing.T) {
	rows := [][]any{{1, "t", "짧은 요약"}}
	out := FormatRows(rows, "id,topic,summary", false)
	want := "1|t|짧은 요약"
	if strings.TrimRight(out, "\n") != want {
		t.Errorf("FormatRows = %q, want %q", out, want)
	}
}

// TestFormatRowsJSON — jsonMode is one JSON line per row, only the selected fields keys.
func TestFormatRowsJSON(t *testing.T) {
	rows := [][]any{{7, "주제", strings.Repeat("b", 100)}}
	out := FormatRows(rows, "id,topic", true)
	line := strings.TrimRight(out, "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("JSON 파싱 실패: %v (%q)", err, line)
	}
	if len(m) != 2 {
		t.Errorf("fields=id,topic면 2키 기대: %v", m)
	}
	if _, ok := m["summary"]; ok {
		t.Errorf("선택 안 한 summary 포함됨: %v", m)
	}
}

// TestFormatRowsDefaultFields — with fields blank, the id|topic|summary default.
func TestFormatRowsDefaultFields(t *testing.T) {
	rows := [][]any{{1, "t", "s"}}
	out := FormatRows(rows, "", false)
	if strings.TrimRight(out, "\n") != "1|t|s" {
		t.Errorf("기본 3컬럼 기대: %q", out)
	}
}

// TestFormatRowsBoundary80 — exactly 80 characters passes; only 81 truncates (a review regression).
func TestFormatRowsBoundary80(t *testing.T) {
	rows := [][]any{{1, "t", strings.Repeat("a", 80)}}
	if got := strings.TrimRight(FormatRows(rows, "id,topic,summary", false), "\n"); got != "1|t|"+strings.Repeat("a", 80) {
		t.Errorf("80자가 잘림: %s", got)
	}
}

// TestFormatRowsFieldsNoTruncate — non-summary columns are not truncated when fields-selected (a review regression).
func TestFormatRowsFieldsNoTruncate(t *testing.T) {
	rows := [][]any{{7, strings.Repeat("t", 100), "s"}}
	got := strings.TrimRight(FormatRows(rows, "id,topic", false), "\n")
	want := "7|" + strings.Repeat("t", 100)
	if got != want {
		t.Errorf("fields 선택 출력 = 길이 %d, want %d", len(got), len(want))
	}
}

// TestFormatRowsEmpty — no rows means the empty string (a review regression).
func TestFormatRowsEmpty(t *testing.T) {
	if got := FormatRows(nil, "", false); got != "" {
		t.Errorf("빈 결과 출력 = %q, want 빈 문자열", got)
	}
}

// TestBuildReadSelectsAllFields — all 8 columns SELECTed to support the --fields contract (a review regression).
func TestBuildReadSelectsAllFields(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	for _, c := range []string{"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status"} {
		if !strings.Contains(sql, c) {
			t.Errorf("SELECT에 %q 누락: %s", c, sql)
		}
	}
}

// TestFormatRowsFieldMapping — from an 8-column row, entity etc. are selected exactly by name mapping (a positional-drift regression).
func TestFormatRowsFieldMapping(t *testing.T) {
	rows := [][]any{{1, "t", "s", ":a:b:", "ent", "dom", 0.9, "active"}}
	got := strings.TrimRight(FormatRows(rows, "entity,domain", false), "\n")
	if got != "ent|dom" {
		t.Errorf("fields 이름 매핑 = %q, want ent|dom", got)
	}
}

// TestRankOrderParameterized — q is a parameter ($N) — the inline
// literal removed (settling the escape dependency). Only the prefix-clause structure is checked exactly.
func TestRankOrderParameterized(t *testing.T) {
	got := RankOrder(5)
	wantPrefix := "ORDER BY (lower(topic)=lower($5)) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(5) = %q, want 접두 %q", got, wantPrefix)
	}
}

// TestRankOrderPlain — a plain q also keeps the parameterized clause prefix
// (exact match -> confidence -> newest) (the leading priorities unchanged even after the trailing tiebreakers).
func TestRankOrderPlain(t *testing.T) {
	got := RankOrder(1)
	wantPrefix := "ORDER BY (lower(topic)=lower($1)) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(%q) = %q, want 접두 %q", "flint", got, wantPrefix)
	}
}

// TestBuildReadUsesRankOrder — BuildRead's sort clause inserts RankOrder (parameterized) as-is.
func TestBuildReadUsesRankOrder(t *testing.T) {
	sql, args := BuildRead("Flint", Filters{})
	// q is the ranking parameter — $2 is the exact-match comparison value (BuildRead appends q
	// after the filter placeholders).
	if !strings.Contains(sql, RankOrder(len(args)-1)) {
		t.Errorf("RankOrder 절 미삽입: %s", sql)
	}
}

// TestFormatRowsUnknownFieldPositional — fields outside the mapping (kind, ts) use the positional fallback (hot_view compatible).
func TestFormatRowsUnknownFieldPositional(t *testing.T) {
	rows := [][]any{{"project", "p", "active", "2026-10-21T00:00:00Z"}}
	got := strings.TrimRight(FormatRows(rows, "kind,topic,summary,ts", false), "\n")
	want := "project|p|active|2026-10-21T00:00:00Z"
	if got != want {
		t.Errorf("위치 fallback = %q, want %q", got, want)
	}
}

// TestReorderByFallbackCountRRF — RRF fusion — multiple signal
// combinations score the matrix differently from sequential tiebreaks (even with a lower match
// count, a larger read_count + jamo signal sum ranks higher).
func TestReorderByFallbackCountRRF(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업 정책", "매일 자동", "", "", "", 0.9, "active", 0},     // 2 exact, read 0
		{int32(2), "백업 절차", "주간 수동 점검", "", "", "", 0.8, "active", 30}, // 1 exact, read 30, jamo 2
		{int32(3), "포트", "5433", "", "", "", 0.7, "active", 100},       // 0 exact, read 100
	}
	ReorderByFallbackCount(rows, []string{"백업", "자동"})
	if rows[0][0] != int32(2) {
		t.Errorf("RRF 상위 = %v, want 2(3신호 융합 합산 최고)", rows[0][0])
	}
	if rows[2][0] != int32(3) {
		t.Errorf("최하위 = %v, want 3(정합·자모 0은 read 100 단일 신호로 못 이긴다)", rows[2][0])
	}
}

// TestJamoOverlap — jamo bigrams absorb inflectional variation.
func TestJamoOverlap(t *testing.T) {
	if jamoOverlap(jamoTokenSets([]string{"백업"}), "백업용 스크립트", "") == 0 {
		t.Error("'백업' ↔ '백업용' 자모 bigram 미매칭")
	}
	if jamoOverlap(jamoTokenSets([]string{"포트"}), "환경설정", "") != 0 {
		t.Error("무관 문자열 매칭 오판")
	}
}

// TestReorderByTopicSignal — the 4th signal, topic match — a row with a dominant
// read_count (whose conventional RankOrder topic priority used to be lost in fallback
// fusion) is beaten by the topic-matching row.
func TestReorderByTopicSignal(t *testing.T) {
	rows := [][]any{
		{int32(1), "flint 백업 복구 절차", "요약", "", "", "", 0.9, "active", int32(6)},
		{int32(2), "flint v0.10.0 스코프·버전 결정", "백업 신뢰성 언급", "", "", "", 0.8, "active", int32(109)},
	}
	ReorderByFallbackCount(rows, []string{"백업", "절차"})
	if rows[0][0] != int32(1) {
		t.Errorf("topic 정합 행 우선 위반: 상위=%v", rows[0][0])
	}
}

// ── v1.16.0 T1 — ReorderByTokenScore: the phrase-hit in-memory rerank. ──

// tokenScoreColIdx — the colIdx fixture of the read result rows (the
// ReadColumnIndexMap shape; explicit here so the pure function is tested
// independent of the readColumns source).
var tokenScoreColIdx = map[string]int{"topic": 1, "summary": 2}

// rowIDsOf — the id column of rows (assertion helper).
func rowIDsOf(rows [][]any) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = fmt.Sprint(r[0])
	}
	return ids
}

// TestReorderByTokenScoreTiers — the per-token tier hierarchy: topic
// prefix(4) > summary prefix(3) > topic contains(2) > summary contains(1),
// each matched token adding a +1 cover bonus. A reverse-ordered fixture
// comes back fully tier-sorted.
func TestReorderByTokenScoreTiers(t *testing.T) {
	rows := [][]any{
		{"5", "복구", "정책", "", "", "", "", "", ""},          // no match — 0
		{"4", "메모", "매일 백업 절차 안내", "", "", "", "", "", ""}, // summary contains — 2
		{"3", "우리 백업 절차", "기타", "", "", "", "", "", ""},    // topic contains — 3
		{"2", "메모", "백업 절차", "", "", "", "", "", ""},       // summary prefix — 4
		{"1", "백업 절차", "기타", "", "", "", "", "", ""},       // topic prefix — 5
	}
	got := ReorderByTokenScore(rows, "백업", tokenScoreColIdx)
	want := []string{"1", "2", "3", "4", "5"}
	for i, w := range want {
		if g := fmt.Sprint(got[i][0]); g != w {
			t.Fatalf("tier 순서[%d]=%s, want %s(전체 %v)", i, g, w, rowIDsOf(got))
		}
	}
}

// TestReorderByTokenScoreMultiTokenCover — the cover-count bonus: two
// summary-covered tokens (2+2=4) beat a single topic-contained token
// (2+1=3), while a single topic prefix (4+1=5) still tops both.
func TestReorderByTokenScoreMultiTokenCover(t *testing.T) {
	rows := [][]any{
		{"2", "우리 백업 수칙", "메모", "", "", "", "", "", ""},         // first token — topic contains — 3
		{"1", "가이드", "매일 백업과 자동 점검 안내", "", "", "", "", "", ""}, // both tokens summary contains — 4
		{"3", "자동 점검", "기타", "", "", "", "", "", ""},            // second token — topic prefix — 5
	}
	got := ReorderByTokenScore(rows, "백업 자동", tokenScoreColIdx)
	want := []string{"3", "1", "2"}
	for i, w := range want {
		if g := fmt.Sprint(got[i][0]); g != w {
			t.Fatalf("커버 가산 순서[%d]=%s, want %s(전체 %v)", i, g, w, rowIDsOf(got))
		}
	}
}

// TestReorderByTokenScoreTieStableAndNoToken — equal scores keep the input
// (= SQL RankOrder) order; a query without tokens (empty·punctuation-only)
// leaves the rows unchanged.
func TestReorderByTokenScoreTieStableAndNoToken(t *testing.T) {
	rows := [][]any{
		{"1", "백업 A", "x", "", "", "", "", "", ""},
		{"2", "백업 B", "y", "", "", "", "", "", ""},
		{"3", "백업 C", "z", "", "", "", "", "", ""},
	}
	got := ReorderByTokenScore(rows, "백업", tokenScoreColIdx)
	for i, w := range []string{"1", "2", "3"} {
		if g := fmt.Sprint(got[i][0]); g != w {
			t.Fatalf("동률[%d]=%s, want %s(SQL 순서 유지 — 전체 %v)", i, g, w, rowIDsOf(got))
		}
	}
	for _, q := range []string{"", "   ", "!!!"} {
		got := ReorderByTokenScore(rows, q, tokenScoreColIdx)
		if fmt.Sprint(got[0][0]) != "1" || fmt.Sprint(got[2][0]) != "3" {
			t.Errorf("토큰 없는 q %q가 순서 변경 — %v", q, rowIDsOf(got))
		}
	}
}

// TestReorderByTokenScoreNFKCCaseWhitespace — both sides are normalized
// (NFKC·lower·whitespace fold): an NFD-decomposed query still prefixes the
// NFC topic; mixed case and inner whitespace runs match the folded row.
func TestReorderByTokenScoreNFKCCaseWhitespace(t *testing.T) {
	q := norm.NFD.String("백업")
	if q == "백업" {
		t.Fatal("픽스처 가정 붕괴 — NFD 분해 미발생")
	}
	rows := [][]any{
		{"2", "메모 백업", "기타", "", "", "", "", "", ""}, // contains — 3
		{"1", "백업 정책", "기타", "", "", "", "", "", ""}, // prefix — 5
	}
	got := ReorderByTokenScore(rows, q, tokenScoreColIdx)
	if g := fmt.Sprint(got[0][0]); g != "1" {
		t.Errorf("NFKC 동치 위반 — 상위=%s, want 1(NFD 쿼리→NFC topic 접두)", g)
	}
	// case + inner whitespace runs: "flint  release" folds onto "Flint Release".
	rows2 := [][]any{
		{"2", "기타", "메모 flint release", "", "", "", "", "", ""}, // 2×summary contains — 4+2=6
		{"1", "flint  release 노트", "x", "", "", "", "", "", ""}, // prefix+contains — 5+3=8
	}
	got2 := ReorderByTokenScore(rows2, "Flint Release", tokenScoreColIdx)
	if g := fmt.Sprint(got2[0][0]); g != "1" {
		t.Errorf("대소문자·공백 폴드 위반 — 상위=%s, want 1", g)
	}
}

// TestReorderByTokenScoreRobustness — a missing colIdx (nil map) or a row
// shorter than the column index never panics: the affected side scores 0
// and the sort stays stable.
func TestReorderByTokenScoreRobustness(t *testing.T) {
	rows := [][]any{{"1", "백업", "x"}, {"2", "백업 더", "y"}}
	got := ReorderByTokenScore(rows, "백업", nil) // no columns — all 0, order kept
	if fmt.Sprint(got[0][0]) != "1" || fmt.Sprint(got[1][0]) != "2" {
		t.Errorf("nil colIdx 순서 변경 — %v", rowIDsOf(got))
	}
	// a short row (no topic/summary cells) scores 0 below the matching row.
	short := [][]any{{"1"}, {"2", "백업", "y"}}
	got2 := ReorderByTokenScore(short, "백업", tokenScoreColIdx)
	if fmt.Sprint(got2[0][0]) != "2" || fmt.Sprint(got2[1][0]) != "1" {
		t.Errorf("짧은 행 처리 — %v, want [2 1]", rowIDsOf(got2))
	}
	// fewer than 2 rows — returned as-is.
	if got3 := ReorderByTokenScore([][]any{{"1", "백업", "x"}}, "백업", tokenScoreColIdx); len(got3) != 1 {
		t.Errorf("1행 입력 변형: %v", got3)
	}
}

// TestReadColumnIndexMap — the exported map matches the readColumns
// positions (the caller-side input of ReorderByTokenScore — single source).
func TestReadColumnIndexMap(t *testing.T) {
	m := ReadColumnIndexMap()
	if m["topic"] != 1 || m["summary"] != 2 || m["id"] != 0 || m["read_count"] != 8 {
		t.Errorf("ReadColumnIndexMap 위치 오류: %v", m)
	}
}

// TestRRFFuseDirect — direct assertions on the rrfFuse math
// contract — tied competing ranks, zero-value non-contribution, and a k override changing the order.
func TestRRFFuseDirect(t *testing.T) {
	// ties: in sig1, a and b share rank 2, c is lower (1) — a and b tie, c contributes 0.
	sc := rrfFuse([][]int{{2, 0}, {2, 0}, {1, 0}})
	if sc[0] != sc[1] {
		t.Errorf("동률 행 스코어 불일치: %v", sc)
	}
	if sc[2] >= sc[0] {
		t.Errorf("후순위가 동률 최상위와 같거나 큼: %v", sc)
	}
	// zero-value non-contribution: all signals 0 means score 0.
	if got := rrfFuse([][]int{{0, 0}, {0, 0}}); got[0] != 0 || got[1] != 0 {
		t.Errorf("0값 행이 신호 기여: %v", got)
	}
	// k override: a smaller k widens the rank gaps — multi-signal advantage is amplified.
	prev := rrfK
	t.Cleanup(func() { rrfK = prev })
	// an asymmetric fixture (one signal dominates all) — shrinking k widens the absolute gap.
	rrfK = 1
	tight := rrfFuse([][]int{{2, 3}, {1, 1}})
	rrfK = 60
	loose := rrfFuse([][]int{{2, 3}, {1, 1}})
	if tight[0]-tight[1] <= loose[0]-loose[1] {
		t.Errorf("k 축소가 격차를 확대하지 않음: k1=%v k60=%v", tight, loose)
	}
}
