package query

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestClampLimit — limit 기본 10, 상한 50.
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

// TestBuildReadUsesActiveKnowledge — 읽기는 active_knowledge 뷰로 강제한다.
func TestBuildReadUsesActiveKnowledge(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	if !strings.Contains(sql, "active_knowledge") {
		t.Errorf("BuildRead SQL이 active_knowledge 뷰를 사용해야 함: %s", sql)
	}
}

// TestBuildReadEscapesLike — q의 %·_는 LIKE 와일드카드로 해석되지 않는다
// (WHERE는 placeholder 주입). 랭킹 절(T56)에 인라인되는 복사본은 작은따옴표가
// ”로 이스케이프된 형태만 허용한다.
func TestBuildReadEscapesLike(t *testing.T) {
	sql, args := BuildRead("100%_drop'", Filters{})
	if !strings.Contains(sql, "LIKE lower($1)") {
		t.Errorf("LIKE lower($1) 패턴 누락: %s", sql)
	}
	if !strings.Contains(sql, `lower('100%_drop''')`) {
		t.Errorf("랭킹 절 이스케이프 인라인 누락: %s", sql)
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

// TestBuildReadFilters — tag/entity/domain 필터가 placeholder로 조립된다.
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

// TestBuildReadLimitParam — limit은 ClampLimit 결과가 SQL에 반영된다.
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

// TestFormatRowsTruncates — summary는 rune 기반 80자 절단(한글 안전).
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

// TestFormatRowsShortSummary — 80자 미만은 그대로.
func TestFormatRowsShortSummary(t *testing.T) {
	rows := [][]any{{1, "t", "짧은 요약"}}
	out := FormatRows(rows, "id,topic,summary", false)
	want := "1|t|짧은 요약"
	if strings.TrimRight(out, "\n") != want {
		t.Errorf("FormatRows = %q, want %q", out, want)
	}
}

// TestFormatRowsJSON — jsonMode는 행별 한 줄 JSON, 선택 fields 키만.
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

// TestFormatRowsDefaultFields — fields 공백 시 id|topic|summary 기본.
func TestFormatRowsDefaultFields(t *testing.T) {
	rows := [][]any{{1, "t", "s"}}
	out := FormatRows(rows, "", false)
	if strings.TrimRight(out, "\n") != "1|t|s" {
		t.Errorf("기본 3컬럼 기대: %q", out)
	}
}

// TestFormatRowsBoundary80 — 정확히 80자는 그대로, 81자만 절단(리뷰 회귀).
func TestFormatRowsBoundary80(t *testing.T) {
	rows := [][]any{{1, "t", strings.Repeat("a", 80)}}
	if got := strings.TrimRight(FormatRows(rows, "id,topic,summary", false), "\n"); got != "1|t|"+strings.Repeat("a", 80) {
		t.Errorf("80자가 잘림: %s", got)
	}
}

// TestFormatRowsFieldsNoTruncate — summary 외 컬럼은 fields 선택 시 무절단(리뷰 회귀).
func TestFormatRowsFieldsNoTruncate(t *testing.T) {
	rows := [][]any{{7, strings.Repeat("t", 100), "s"}}
	got := strings.TrimRight(FormatRows(rows, "id,topic", false), "\n")
	want := "7|" + strings.Repeat("t", 100)
	if got != want {
		t.Errorf("fields 선택 출력 = 길이 %d, want %d", len(got), len(want))
	}
}

// TestFormatRowsEmpty — 행이 없으면 빈 문자열(리뷰 회귀).
func TestFormatRowsEmpty(t *testing.T) {
	if got := FormatRows(nil, "", false); got != "" {
		t.Errorf("빈 결과 출력 = %q, want 빈 문자열", got)
	}
}

// TestBuildReadSelectsAllFields — --fields 계약 지원을 위해 8컬럼 전부 SELECT(리뷰 회귀).
func TestBuildReadSelectsAllFields(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	for _, c := range []string{"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status"} {
		if !strings.Contains(sql, c) {
			t.Errorf("SELECT에 %q 누락: %s", c, sql)
		}
	}
}

// TestFormatRowsFieldMapping — 8컬럼 행에서 entity 등은 이름 매핑으로 정확히 선택(위치 어긋남 회귀).
func TestFormatRowsFieldMapping(t *testing.T) {
	rows := [][]any{{1, "t", "s", ":a:b:", "ent", "dom", 0.9, "active"}}
	got := strings.TrimRight(FormatRows(rows, "entity,domain", false), "\n")
	if got != "ent|dom" {
		t.Errorf("fields 이름 매핑 = %q, want ent|dom", got)
	}
}

// TestRankOrderEscapes — q의 작은따옴표는 ”로 이스케이프(인젝션 차단, T56).
// v0.6(T68) tie-breaker 접미 허용 — 접두(이스케이프 포함 기존 절)만 정확 검사.
func TestRankOrderEscapes(t *testing.T) {
	got := RankOrder("a'b")
	wantPrefix := "ORDER BY (lower(topic)=lower('a''b')) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(%q) = %q, want 접두 %q", "a'b", got, wantPrefix)
	}
}

// TestRankOrderPlain — 평범한 q는 기존 절(정확일치 → confidence → 최신순)을
// 접두로 그대로 유지(T68 접미 tie-breaker 이후에도 선행 순위 불변).
func TestRankOrderPlain(t *testing.T) {
	got := RankOrder("flint")
	wantPrefix := "ORDER BY (lower(topic)=lower('flint')) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(%q) = %q, want 접두 %q", "flint", got, wantPrefix)
	}
}

// TestBuildReadUsesRankOrder — BuildRead의 정렬 절은 RankOrder(q)를 그대로 삽입.
func TestBuildReadUsesRankOrder(t *testing.T) {
	sql, _ := BuildRead("Flint", Filters{})
	if !strings.Contains(sql, RankOrder("Flint")) {
		t.Errorf("RankOrder 절 미삽입: %s", sql)
	}
}

// TestFormatRowsUnknownFieldPositional — 매핑 밖 필드(kind·ts)는 위치 fallback(hot_view 호환).
func TestFormatRowsUnknownFieldPositional(t *testing.T) {
	rows := [][]any{{"project", "p", "active", "2026-10-21T00:00:00Z"}}
	got := strings.TrimRight(FormatRows(rows, "kind,topic,summary,ts", false), "\n")
	want := "project|p|active|2026-10-21T00:00:00Z"
	if got != want {
		t.Errorf("위치 fallback = %q, want %q", got, want)
	}
}

// TestReorderByFallbackCountRRF — v0.25.0 R1: RRF 융합 — 다중 신호
// 조합이 순차 타이브레이크와 다르게 행렬을 매긴다(정합수 낮아도
// read_count·자모 신호 합산이 크면 상위).
func TestReorderByFallbackCountRRF(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업 정책", "매일 자동", "", "", "", 0.9, "active", 0},     // 정합 2·read 0
		{int32(2), "백업 절차", "주간 수동 점검", "", "", "", 0.8, "active", 30}, // 정합 1·read 30·자모 2
		{int32(3), "포트", "5433", "", "", "", 0.7, "active", 100},       // 정합 0·read 100
	}
	ReorderByFallbackCount(rows, []string{"백업", "자동"})
	if rows[0][0] != int32(2) {
		t.Errorf("RRF 상위 = %v, want 2(3신호 융합 합산 최고)", rows[0][0])
	}
	if rows[2][0] != int32(3) {
		t.Errorf("최하위 = %v, want 3(정합·자모 0은 read 100 단일 신호로 못 이긴다)", rows[2][0])
	}
}

// TestJamoOverlap — v0.25.0 R2: 자모 bigram이 어미 변형을 흡수한다.
func TestJamoOverlap(t *testing.T) {
	if jamoOverlap(jamoTokenSets([]string{"백업"}), "백업용 스크립트", "") == 0 {
		t.Error("'백업' ↔ '백업용' 자모 bigram 미매칭")
	}
	if jamoOverlap(jamoTokenSets([]string{"포트"}), "환경설정", "") != 0 {
		t.Error("무관 문자열 매칭 오판")
	}
}

// TestReorderByTopicSignal — v0.25.1: topic 정합 제4신호 — read_count
// 지배적 행(관례 RankOrder의 topic 우선이 폴백 융합에서 소실되던 것)을
// topic 정합 행이 이긴다.
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

// TestRRFFuseDirect — v0.25.3(2차 리뷰 ④): rrfFuse 수학 계약 직접
// 단얜 — 동률 경쟁 순위·0값 미기여·k 오버라이드가 순서를 바꾼다.
func TestRRFFuseDirect(t *testing.T) {
	// 동률: sig1에서 a·b 동순위(2), c 후순(1) — a·b 동점, c 미기여(0).
	sc := rrfFuse([][]int{{2, 0}, {2, 0}, {1, 0}})
	if sc[0] != sc[1] {
		t.Errorf("동률 행 스코어 불일치: %v", sc)
	}
	if sc[2] >= sc[0] {
		t.Errorf("후순위가 동률 최상위와 같거나 큼: %v", sc)
	}
	// 0값 미기여: 전 신호 0이면 스코어 0.
	if got := rrfFuse([][]int{{0, 0}, {0, 0}}); got[0] != 0 || got[1] != 0 {
		t.Errorf("0값 행이 신호 기여: %v", got)
	}
	// k 오버라이드: k 작으면 순위 격차 확대 — 다중 신호 우위가 강화된다.
	prev := rrfK
	t.Cleanup(func() { rrfK = prev })
	// 비대칭 픽스처(전 신호 동일 우위) — k 축소가 절대 격차를 확대.
	rrfK = 1
	tight := rrfFuse([][]int{{2, 3}, {1, 1}})
	rrfK = 60
	loose := rrfFuse([][]int{{2, 3}, {1, 1}})
	if tight[0]-tight[1] <= loose[0]-loose[1] {
		t.Errorf("k 축소가 격차를 확대하지 않음: k1=%v k60=%v", tight, loose)
	}
}
