package query

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

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

func TestBuildReadUsesActiveKnowledge(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	if !strings.Contains(sql, "active_knowledge") {
		t.Errorf("BuildRead SQL이 active_knowledge 뷰를 사용해야 함: %s", sql)
	}
}

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

func TestFormatRowsShortSummary(t *testing.T) {
	rows := [][]any{{1, "t", "짧은 요약"}}
	out := FormatRows(rows, "id,topic,summary", false)
	want := "1|t|짧은 요약"
	if strings.TrimRight(out, "\n") != want {
		t.Errorf("FormatRows = %q, want %q", out, want)
	}
}

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

func TestFormatRowsDefaultFields(t *testing.T) {
	rows := [][]any{{1, "t", "s"}}
	out := FormatRows(rows, "", false)
	if strings.TrimRight(out, "\n") != "1|t|s" {
		t.Errorf("기본 3컬럼 기대: %q", out)
	}
}

func TestFormatRowsBoundary80(t *testing.T) {
	rows := [][]any{{1, "t", strings.Repeat("a", 80)}}
	if got := strings.TrimRight(FormatRows(rows, "id,topic,summary", false), "\n"); got != "1|t|"+strings.Repeat("a", 80) {
		t.Errorf("80자가 잘림: %s", got)
	}
}

func TestFormatRowsFieldsNoTruncate(t *testing.T) {
	rows := [][]any{{7, strings.Repeat("t", 100), "s"}}
	got := strings.TrimRight(FormatRows(rows, "id,topic", false), "\n")
	want := "7|" + strings.Repeat("t", 100)
	if got != want {
		t.Errorf("fields 선택 출력 = 길이 %d, want %d", len(got), len(want))
	}
}

func TestFormatRowsEmpty(t *testing.T) {
	if got := FormatRows(nil, "", false); got != "" {
		t.Errorf("빈 결과 출력 = %q, want 빈 문자열", got)
	}
}

func TestBuildReadSelectsAllFields(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	for _, c := range []string{"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status"} {
		if !strings.Contains(sql, c) {
			t.Errorf("SELECT에 %q 누락: %s", c, sql)
		}
	}
}

func TestFormatRowsFieldMapping(t *testing.T) {
	rows := [][]any{{1, "t", "s", ":a:b:", "ent", "dom", 0.9, "active"}}
	got := strings.TrimRight(FormatRows(rows, "entity,domain", false), "\n")
	if got != "ent|dom" {
		t.Errorf("fields 이름 매핑 = %q, want ent|dom", got)
	}
}

func TestRankOrderParameterized(t *testing.T) {
	got := RankOrder(5)
	wantPrefix := "ORDER BY (lower(topic)=lower($5)) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(5) = %q, want 접두 %q", got, wantPrefix)
	}
}

func TestRankOrderPlain(t *testing.T) {
	got := RankOrder(1)
	wantPrefix := "ORDER BY (lower(topic)=lower($1)) DESC, confidence DESC, ingested_at DESC"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("RankOrder(%q) = %q, want 접두 %q", "flint", got, wantPrefix)
	}
}

func TestBuildReadUsesRankOrder(t *testing.T) {
	sql, args := BuildRead("Flint", Filters{})

	if !strings.Contains(sql, RankOrder(len(args)-1)) {
		t.Errorf("RankOrder 절 미삽입: %s", sql)
	}
}

func TestFormatRowsUnknownFieldPositional(t *testing.T) {
	rows := [][]any{{"project", "p", "active", "2026-10-21T00:00:00Z"}}
	got := strings.TrimRight(FormatRows(rows, "kind,topic,summary,ts", false), "\n")
	want := "project|p|active|2026-10-21T00:00:00Z"
	if got != want {
		t.Errorf("위치 fallback = %q, want %q", got, want)
	}
}

func TestReorderByFallbackCountRRF(t *testing.T) {
	rows := [][]any{
		{int32(1), "백업 정책", "매일 자동", "", "", "", 0.9, "active", 0},
		{int32(2), "백업 절차", "주간 수동 점검", "", "", "", 0.8, "active", 30},
		{int32(3), "포트", "5433", "", "", "", 0.7, "active", 100},
	}
	ReorderByFallbackCount(rows, []string{"백업", "자동"})
	if rows[0][0] != int32(2) {
		t.Errorf("RRF 상위 = %v, want 2(3신호 융합 합산 최고)", rows[0][0])
	}
	if rows[2][0] != int32(3) {
		t.Errorf("최하위 = %v, want 3(정합·자모 0은 read 100 단일 신호로 못 이긴다)", rows[2][0])
	}
}

func TestJamoOverlap(t *testing.T) {
	if jamoOverlap(jamoTokenSets([]string{"백업"}), "백업용 스크립트", "") == 0 {
		t.Error("'백업' ↔ '백업용' 자모 bigram 미매칭")
	}
	if jamoOverlap(jamoTokenSets([]string{"포트"}), "환경설정", "") != 0 {
		t.Error("무관 문자열 매칭 오판")
	}
}

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

var tokenScoreColIdx = map[string]int{"topic": 1, "summary": 2}

func rowIDsOf(rows [][]any) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = fmt.Sprint(r[0])
	}
	return ids
}

func TestReorderByTokenScoreTiers(t *testing.T) {
	rows := [][]any{
		{"5", "복구", "정책", "", "", "", "", "", ""},
		{"4", "메모", "매일 백업 절차 안내", "", "", "", "", "", ""},
		{"3", "우리 백업 절차", "기타", "", "", "", "", "", ""},
		{"2", "메모", "백업 절차", "", "", "", "", "", ""},
		{"1", "백업 절차", "기타", "", "", "", "", "", ""},
	}
	got := ReorderByTokenScore(rows, "백업", tokenScoreColIdx)
	want := []string{"1", "2", "3", "4", "5"}
	for i, w := range want {
		if g := fmt.Sprint(got[i][0]); g != w {
			t.Fatalf("tier 순서[%d]=%s, want %s(전체 %v)", i, g, w, rowIDsOf(got))
		}
	}
}

func TestReorderByTokenScoreMultiTokenCover(t *testing.T) {
	rows := [][]any{
		{"2", "우리 백업 수칙", "메모", "", "", "", "", "", ""},
		{"1", "가이드", "매일 백업과 자동 점검 안내", "", "", "", "", "", ""},
		{"3", "자동 점검", "기타", "", "", "", "", "", ""},
	}
	got := ReorderByTokenScore(rows, "백업 자동", tokenScoreColIdx)
	want := []string{"3", "1", "2"}
	for i, w := range want {
		if g := fmt.Sprint(got[i][0]); g != w {
			t.Fatalf("커버 가산 순서[%d]=%s, want %s(전체 %v)", i, g, w, rowIDsOf(got))
		}
	}
}

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

func TestReorderByTokenScoreNFKCCaseWhitespace(t *testing.T) {
	q := norm.NFD.String("백업")
	if q == "백업" {
		t.Fatal("픽스처 가정 붕괴 — NFD 분해 미발생")
	}
	rows := [][]any{
		{"2", "메모 백업", "기타", "", "", "", "", "", ""},
		{"1", "백업 정책", "기타", "", "", "", "", "", ""},
	}
	got := ReorderByTokenScore(rows, q, tokenScoreColIdx)
	if g := fmt.Sprint(got[0][0]); g != "1" {
		t.Errorf("NFKC 동치 위반 — 상위=%s, want 1(NFD 쿼리→NFC topic 접두)", g)
	}

	rows2 := [][]any{
		{"2", "기타", "메모 flint release", "", "", "", "", "", ""},
		{"1", "flint  release 노트", "x", "", "", "", "", "", ""},
	}
	got2 := ReorderByTokenScore(rows2, "Flint Release", tokenScoreColIdx)
	if g := fmt.Sprint(got2[0][0]); g != "1" {
		t.Errorf("대소문자·공백 폴드 위반 — 상위=%s, want 1", g)
	}
}

func TestReorderByTokenScoreRobustness(t *testing.T) {
	rows := [][]any{{"1", "백업", "x"}, {"2", "백업 더", "y"}}
	got := ReorderByTokenScore(rows, "백업", nil)
	if fmt.Sprint(got[0][0]) != "1" || fmt.Sprint(got[1][0]) != "2" {
		t.Errorf("nil colIdx 순서 변경 — %v", rowIDsOf(got))
	}

	short := [][]any{{"1"}, {"2", "백업", "y"}}
	got2 := ReorderByTokenScore(short, "백업", tokenScoreColIdx)
	if fmt.Sprint(got2[0][0]) != "2" || fmt.Sprint(got2[1][0]) != "1" {
		t.Errorf("짧은 행 처리 — %v, want [2 1]", rowIDsOf(got2))
	}

	if got3 := ReorderByTokenScore([][]any{{"1", "백업", "x"}}, "백업", tokenScoreColIdx); len(got3) != 1 {
		t.Errorf("1행 입력 변형: %v", got3)
	}
}

func TestReadColumnIndexMap(t *testing.T) {
	m := ReadColumnIndexMap()
	if m["topic"] != 1 || m["summary"] != 2 || m["id"] != 0 || m["read_count"] != 8 {
		t.Errorf("ReadColumnIndexMap 위치 오류: %v", m)
	}
}

func TestRRFFuseDirect(t *testing.T) {

	sc := rrfFuse([][]int{{2, 0}, {2, 0}, {1, 0}})
	if sc[0] != sc[1] {
		t.Errorf("동률 행 스코어 불일치: %v", sc)
	}
	if sc[2] >= sc[0] {
		t.Errorf("후순위가 동률 최상위와 같거나 큼: %v", sc)
	}

	if got := rrfFuse([][]int{{0, 0}, {0, 0}}); got[0] != 0 || got[1] != 0 {
		t.Errorf("0값 행이 신호 기여: %v", got)
	}

	prev := rrfK
	t.Cleanup(func() { rrfK = prev })

	rrfK = 1
	tight := rrfFuse([][]int{{2, 3}, {1, 1}})
	rrfK = 60
	loose := rrfFuse([][]int{{2, 3}, {1, 1}})
	if tight[0]-tight[1] <= loose[0]-loose[1] {
		t.Errorf("k 축소가 격차를 확대하지 않음: k1=%v k60=%v", tight, loose)
	}
}

func TestBuildReadMinConfidenceFilter(t *testing.T) {
	for _, v := range []float64{0, 0.5, 1} {
		f := Filters{MinConfidence: &v}
		sql, args := BuildRead("", f)
		if !strings.Contains(sql, "confidence >= $") {
			t.Errorf("min-confidence %v: confidence >= $n 절 누락: %s", v, sql)
		}
		found := false
		for _, a := range args {
			if fv, ok := a.(float64); ok && fv == v {
				found = true
			}
		}
		if !found {
			t.Errorf("min-confidence %v: 값이 args에 없음: %v", v, args)
		}
	}
}

func TestBuildReadMinConfidenceAbsent(t *testing.T) {
	sql, _ := BuildRead("", Filters{})
	if strings.Contains(sql, "confidence >=") {
		t.Errorf("미지정 min-confidence가 절을 추가함: %s", sql)
	}
}

func TestBuildReadOriginFilter(t *testing.T) {
	for _, v := range []string{"agent", "human"} {
		sql, args := BuildRead("", Filters{Origin: v})
		if !strings.Contains(sql, "origin = $") {
			t.Errorf("origin %q: origin = $n 절 누락: %s", v, sql)
		}
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == v {
				found = true
			}
		}
		if !found {
			t.Errorf("origin %q: 값이 args에 없음: %v", v, args)
		}
	}
}

func TestBuildReadNewFiltersCombined(t *testing.T) {
	mc := 0.7
	f := Filters{Tag: "a", Entity: "user", Domain: "game", MinConfidence: &mc, Origin: "agent"}
	sql, args := BuildRead("q", f)
	for _, frag := range []string{
		"LIKE lower($1)",
		"strpos(tags, ':' || $2 || ':') > 0",
		"entity = $3",
		"domain = $4",
		"confidence >= $5",
		"origin = $6",
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("조합 필터 조각 %q 누락: %s", frag, sql)
		}
	}
	if len(args) < 6 || args[4] != 0.7 || args[5] != "agent" {
		t.Errorf("조합 args = %v, want [q a user game 0.7 agent ...]", args)
	}
}

func TestBuildReadStatusNewFilters(t *testing.T) {
	mc := 0.5
	sql, args := BuildReadStatus("", Filters{Status: "active", MinConfidence: &mc, Origin: "human"})
	for _, frag := range []string{"status = $1", "confidence >= $2", "origin = $3"} {
		if !strings.Contains(sql, frag) {
			t.Errorf("status 경로 필터 조각 %q 누락: %s", frag, sql)
		}
	}
	if len(args) < 3 || args[0] != "active" || args[1] != 0.5 || args[2] != "human" {
		t.Errorf("status 경로 args = %v", args)
	}
}

func TestVaultMaxRowsConstant(t *testing.T) {
	if VaultMaxRows != 5 {
		t.Fatalf("VaultMaxRows = %d, want 5(플랜 계약 상수)", VaultMaxRows)
	}
}

func TestBuildVaultReadShape(t *testing.T) {
	sql, args := BuildVaultRead("데모 노트")
	for _, frag := range []string{
		"FROM vault_files",
		"lower(title || ' ' || summary || ' ' || tags) LIKE lower($1)",
		"ORDER BY path",
		"LIMIT 5",
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("vault 쿼리 조각 %q 누락: %s", frag, sql)
		}
	}
	if pat, ok := args[0].(string); !ok || pat != "%데모 노트%" {
		t.Errorf("LIKE 패턴 인자 = %v, want %%데모 노트%%", args)
	}
}

func TestBuildVaultReadEscapesLike(t *testing.T) {
	_, args := BuildVaultRead("100%_drop'")
	if pat, ok := args[0].(string); !ok || pat != `%100\%\_drop'%` {
		t.Errorf("이스케이프 패턴 = %v, want %%100\\%%\\_drop'%%", args)
	}
}

func TestFormatVaultRows(t *testing.T) {
	rows := [][]any{{"데모 제목", strings.Repeat("요", 100), ":demo:", "/tmp/demo-vault/note.md"}}
	out := FormatVaultRows(rows, false)
	want := "vault|/tmp/demo-vault/note.md|데모 제목|" + strings.Repeat("요", summaryMax) + "\n"
	if out != want {
		t.Errorf("FormatVaultRows(text) = %q, want %q", out, want)
	}
	jout := FormatVaultRows(rows, true)
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimRight(jout, "\n")), &m); err != nil {
		t.Fatalf("json 파싱: %v: %q", err, jout)
	}
	if m["source"] != "vault" || m["path"] != "/tmp/demo-vault/note.md" || m["title"] != "데모 제목" {
		t.Errorf("json 식별 필드 = %v", m)
	}
	if got := len([]rune(m["summary"])); got != summaryMax {
		t.Errorf("json summary 길이 = %d, want %d(토큰 예산)", got, summaryMax)
	}
}

func TestFormatVaultRowsEmpty(t *testing.T) {
	if out := FormatVaultRows(nil, false); out != "" {
		t.Errorf("빈 vault 출력 = %q, want \"\"", out)
	}
}

func TestRelatedMaxRowsConstant(t *testing.T) {
	if RelatedMaxRows != 5 {
		t.Fatalf("RelatedMaxRows = %d, want 5(플랜 계약 상한)", RelatedMaxRows)
	}
}

func TestBuildRelatedReadShape(t *testing.T) {
	sql, args := BuildRelatedRead(7)
	for _, frag := range []string{
		"FROM knowledge_links",
		"l.from_id = $1",
		"l.to_id = $1",
		"k.id = l.to_id::int",
		"k.id = l.from_id::int",
		"'forward'",
		"'reverse'",
		"UNION ALL",
		"ORDER BY id",
		"LIMIT 5",
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("related 쿼리 조각 %q 누락: %s", frag, sql)
		}
	}
	if len(args) != 1 || args[0].(int) != 7 {
		t.Errorf("related args = %v, want [7]", args)
	}
}

func TestFormatRelatedRows(t *testing.T) {
	rows := [][]any{{"3", "데모 이웃 토픽", strings.Repeat("요", 100), "refines", "reverse"}}
	out := FormatRelatedRows(rows, false)
	want := "related|3|refines|reverse|데모 이웃 토픽|" + strings.Repeat("요", summaryMax) + "\n"
	if out != want {
		t.Errorf("FormatRelatedRows(text) = %q, want %q", out, want)
	}
	jout := FormatRelatedRows(rows, true)
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimRight(jout, "\n")), &m); err != nil {
		t.Fatalf("json 파싱: %v: %q", err, jout)
	}
	if m["source"] != "related" || m["id"] != "3" || m["relation"] != "refines" || m["direction"] != "reverse" {
		t.Errorf("json 식별 필드 = %v", m)
	}
	if got := len([]rune(m["summary"])); got != summaryMax {
		t.Errorf("json summary 길이 = %d, want %d(토큰 예산)", got, summaryMax)
	}
}

func TestFormatRelatedRowsNoPipe(t *testing.T) {
	rows := [][]any{{"3", "a|b", "s", "re|fines", "forward"}}
	out := FormatRelatedRows(rows, false)
	if strings.Count(out, "|") != 5 {
		t.Errorf("파이프 폭발 방지 위반: %q", out)
	}
}

func TestFormatRelatedRowsEmpty(t *testing.T) {
	if out := FormatRelatedRows(nil, false); out != "" {
		t.Errorf("빈 related 출력 = %q, want \"\"", out)
	}
}

func TestBuildReadFallbackNewFilters(t *testing.T) {
	mc := 0.8
	sql, args, ok := BuildReadFallback("alpha beta", Filters{MinConfidence: &mc, Origin: "agent"})
	if !ok {
		t.Fatal("fallback 미조립(토큰 2개)")
	}
	if !strings.Contains(sql, "confidence >= $") {
		t.Errorf("fallback에 confidence 절 누락: %s", sql)
	}
	if !strings.Contains(sql, "origin = $") {
		t.Errorf("fallback에 origin 절 누락: %s", sql)
	}
	foundC, foundO := false, false
	for _, a := range args {
		if fv, ok := a.(float64); ok && fv == 0.8 {
			foundC = true
		}
		if s, ok := a.(string); ok && s == "agent" {
			foundO = true
		}
	}
	if !foundC || !foundO {
		t.Errorf("fallback args에 confidence/origin 값 없음: %v", args)
	}
}

func TestTokenWhyEvidenceDirectComparison(t *testing.T) {
	rows := [][]any{
		{"1", "백업 정책", "기타", "", "", "", "", "", ""},
		{"2", "가이드", "매일 백업과 자동 점검 안내", "", "", "", "", "", ""},
		{"3", "우리 백업 수칙", "메모", "", "", "", "", "", ""},
	}
	ev := TokenWhyEvidence(rows, "백업 자동", tokenScoreColIdx)
	want := []struct {
		id, tokens string
		score      int
	}{
		{"1", "topic_prefix×4+cover×1", 5},
		{"2", "summary_contain×1+summary_contain×1+cover×2", 4},
		{"3", "topic_contain×2+cover×1", 3},
	}
	if len(ev) != len(want) {
		t.Fatalf("근거 행 수 = %d, want %d", len(ev), len(want))
	}
	for i, w := range want {
		if ev[i].ID != w.id || ev[i].Tokens != w.tokens || ev[i].TokenScore != w.score {
			t.Errorf("근거[%d] = {id:%s tokens:%s score:%d}, want {%s %s %d}",
				i, ev[i].ID, ev[i].Tokens, ev[i].TokenScore, w.id, w.tokens, w.score)
		}
	}

	reranked := ReorderByTokenScore(rows, "백업 자동", tokenScoreColIdx)
	for i := range ev {
		if ev[i].ID != rowIDsOf(reranked)[i] {
			t.Fatalf("근거 순서[%d]=%s, want rerank 순서 %v", i, ev[i].ID, rowIDsOf(reranked))
		}
	}
}

func TestRRFWhyEvidenceDirectComparison(t *testing.T) {
	SetRRFK(60)
	t.Cleanup(func() { SetRRFK(60) })
	rows := [][]any{
		{"1", "backup policy demo", "synthetic note", "", "", "", "", "active", int32(5)},
		{"2", "demo subject", "backup method synthetic", "", "", "", "", "active", int32(2)},
	}
	ev := RRFWhyEvidence(rows, []string{"backup", "policy"})
	if len(ev) != 2 {
		t.Fatalf("근거 행 수 = %d, want 2", len(ev))
	}
	c1 := 1.0 / (60 + 1)
	c2 := 1.0 / (60 + 2)

	want1 := []RRFSignalEvidence{
		{"match", 2, 1, c1}, {"read_count", 5, 1, c1}, {"jamo", 0, 0, 0}, {"topic", 2, 1, c1},
	}
	score1 := c1 + c1 + c1

	want2 := []RRFSignalEvidence{
		{"match", 1, 2, c2}, {"read_count", 2, 2, c2}, {"jamo", 0, 0, 0}, {"topic", 0, 0, 0},
	}
	score2 := c2 + c2
	for i, want := range [][]RRFSignalEvidence{want1, want2} {
		if len(ev[i].RRF) != len(want) {
			t.Fatalf("행 %d 신호 수 = %d, want %d", i+1, len(ev[i].RRF), len(want))
		}
		for s := range want {
			if ev[i].RRF[s] != want[s] {
				t.Errorf("행 %d 신호[%d] = %+v, want %+v", i+1, s, ev[i].RRF[s], want[s])
			}
		}
	}
	if ev[0].RRFScore != score1 {
		t.Errorf("행 1 rrf_score = %v, want %v", ev[0].RRFScore, score1)
	}
	if ev[1].RRFScore != score2 {
		t.Errorf("행 2 rrf_score = %v, want %v", ev[1].RRFScore, score2)
	}
}

func TestFormatWhyRowsCap(t *testing.T) {
	ev := make([]WhyEvidence, 12)
	for i := range ev {
		ev[i] = WhyEvidence{ID: strconv.Itoa(i + 1), Tokens: "topic_prefix×4+cover×1", TokenScore: 5}
	}
	if n := strings.Count(FormatWhyRows(ev, false), "\n"); n != WhyMaxRows {
		t.Errorf("부록 행 = %d, want %d(총 상한)", n, WhyMaxRows)
	}
}

func TestFormatWhyRowsNoPipe(t *testing.T) {
	got := FormatWhyRows([]WhyEvidence{{ID: "a|b", Tokens: "x|y", TokenScore: 5}}, false)
	if want := "why|id=a b|x y|score=5\n"; got != want {
		t.Errorf("noPipe 인코딩 = %q, want %q", got, want)
	}
}

func TestFormatWhyRowsJSON(t *testing.T) {
	got := FormatWhyRows([]WhyEvidence{{ID: "1", Tokens: "topic_prefix×4+cover×1", TokenScore: 5}}, true)
	want := `{"why":{"id":"1","tokens":"topic_prefix×4+cover×1","token_score":5}}` + "\n"
	if got != want {
		t.Errorf("json 인코딩 = %q, want %q", got, want)
	}
}

func TestFormatWhyZeroLine(t *testing.T) {
	if got := FormatWhyZero("owned_miss", 0.5, false); got != "why|zero_reason=owned_miss|near_score=0.5\n" {
		t.Errorf("zero 텍스트 인코딩 = %q", got)
	}
	want := `{"why":{"zero_reason":"unowned","near_score":0}}` + "\n"
	if got := FormatWhyZero("unowned", 0, true); got != want {
		t.Errorf("zero json 인코딩 = %q, want %q", got, want)
	}
}
