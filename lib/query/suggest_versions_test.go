package query

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func TestNfkcComposesNFD(t *testing.T) {
	decomposed := "한"
	if got := nfkc(decomposed); got != "한" {
		t.Errorf("nfkc(NFD 한) = %q, want 한", got)
	}
	if got := nfkc("flint 백업"); got != "flint 백업" {
		t.Errorf("NFC 불변 위반: %q", got)
	}
}

func TestBigramDice(t *testing.T) {
	if got := bigramDice("백업복구", "백업복구"); got != 1 {
		t.Errorf("동일 문자열 = %v, want 1", got)
	}
	if got := bigramDice("백업", "복구"); got != 0 {
		t.Errorf("공유 bigram 0 = %v, want 0", got)
	}
	if got := bigramDice("flint", "flint"); got != 1 {
		t.Errorf("동일 라틴 = %v, want 1", got)
	}
}

func TestSuggestSimilarTopN(t *testing.T) {
	rows := [][]any{
		{int32(3), "flint 백업 절차", "덤프 복구"},
		{int32(4), "환영인사", "안부 인사"},
	}
	got := SuggestSimilar("백업 복구 절차", rows, 3)
	want := []Suggestion{{ID: 3, Topic: "flint 백업 절차"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestSimilar = %v, want %v", got, want)
	}
}

func TestSuggestMinScoreFiltersMarginal(t *testing.T) {
	q := "환경설정 초기화 방법"

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

func TestBigramDicePublicVariants(t *testing.T) {
	if got := BigramDice("백업복구", "백업복구"); got != 1 {
		t.Errorf("집합판 동일 문자열 = %v, want 1", got)
	}
	if BigramDice("flint v1", "flint v2") != bigramDice("flint v1", "flint v2") {
		t.Error("BigramDice가 내부 bigramDice와 불일치(위임 아님)")
	}
	if got, want := BigramDice("아아아아", "아아"), 1.0; got != want {
		t.Errorf("집합판 반복 bigram = %v, want %v(반복 접힘)", got, want)
	}
	if got, want := BigramDiceMultiset("아아아아", "아아"), 0.5; got != want {
		t.Errorf("다중집합판 반복 bigram = %v, want %v(출현 수 반영)", got, want)
	}
	if got := BigramDiceMultiset("flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다"); got != 1 {
		t.Errorf("다중집합판 동일 문자열 = %v, want 1", got)
	}
	if got := BigramDiceMultiset("", ""); got != 1 {
		t.Errorf("다중집합판 빈/빈 = %v, want 1(동일 취급)", got)
	}
	if got := BigramDiceMultiset("가", "가나다"); got != 0 {
		t.Errorf("다중집합판 짧은/긴 = %v, want 0(비교 불가=이질)", got)
	}
}

func TestBuildSuggestScanCapped(t *testing.T) {
	sql, args := BuildSuggestScan()
	if len(args) != 0 {
		t.Errorf("스캔 인자 = %v, want nil(상한은 상수 인라인)", args)
	}
	m := regexp.MustCompile(`LIMIT (\d+)$`).FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("스캔 SQL에 LIMIT 상한 없음(무상한 전체 스캔): %s", sql)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n < 5000 {
		t.Errorf("스캔 상한 = %s, want ≥ 5000(개인 규모 수천 행 수용): %s", m[1], sql)
	}
}

func TestSuggestDefaultsFromThresholds(t *testing.T) {
	def := store.DefaultThresholds()
	if suggestMinScore != def.SuggestMinScore {
		t.Errorf("suggestMinScore = %v, want %v(thresholds 기본값)", suggestMinScore, def.SuggestMinScore)
	}
	if suggestScanCap != def.SuggestScanCap {
		t.Errorf("suggestScanCap = %d, want %d(thresholds 기본값)", suggestScanCap, def.SuggestScanCap)
	}
}

func TestSetSuggestThresholdsApplies(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	SetSuggestThresholds(0.99, 7)

	sql, _ := BuildSuggestScan()
	m := regexp.MustCompile(`LIMIT (\d+)`).FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("스캔 SQL에 LIMIT 없음: %q", sql)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n != 7 {
		t.Errorf("스캔 상한 = %s, want 7(오버라이드)", m[1])
	}

	rows := [][]any{
		{int32(1), "flint 백업 절차", ""},
		{int32(2), "전혀 다른 주제", "무관 요약"},
	}
	got := SuggestSimilar("flint 백업 절차", rows, 3)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("minScore 0.99 필터 = %+v, want 동일 행 1건만", got)
	}
	if !strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
		t.Errorf("스캔 SQL 형태 변형: %q", sql)
	}
}

func TestSetSuggestThresholdsIgnoresNonPositive(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	before := store.DefaultThresholds()
	SetSuggestThresholds(0, 0)
	if suggestMinScore != before.SuggestMinScore || suggestScanCap != before.SuggestScanCap {
		t.Errorf("0 주입이 값을 덮어씀: min=%v cap=%d", suggestMinScore, suggestScanCap)
	}
	SetSuggestThresholds(-0.5, -1)
	if suggestMinScore != before.SuggestMinScore || suggestScanCap != before.SuggestScanCap {
		t.Errorf("음수 주입이 값을 덮어씀: min=%v cap=%d", suggestMinScore, suggestScanCap)
	}
}

func TestUsageSuggestCountInvariant(t *testing.T) {
	want := map[string]bool{
		"TestNfkcComposesNFD":                        true,
		"TestBigramDice":                             true,
		"TestSuggestSimilarTopN":                     true,
		"TestSuggestMinScoreFiltersMarginal":         true,
		"TestSuggestSimilarKeepsRelevant":            true,
		"TestBigramDicePublicVariants":               true,
		"TestBuildSuggestScanCapped":                 true,
		"TestSuggestDefaultsFromThresholds":          true,
		"TestSetSuggestThresholdsApplies":            true,
		"TestSetSuggestThresholdsIgnoresNonPositive": true,
	}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("테스트 파일 조사 실패: %v", err)
	}
	fset := token.NewFileSet()
	got := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s 파싱 실패: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if want[fd.Name.Name] {
				got++
			}
		}
	}
	if got != len(want) {
		t.Fatalf("suggest_v 계열 카운트 = %d, want %d(통합 전후 불변)", got, len(want))
	}
	leftover, err := filepath.Glob("suggest_v[0-9]*_test.go")
	if err != nil {
		t.Fatalf("접미 잔여 조사 실패: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("suggest_v 접미 파일 잔여 %d건: %v", len(leftover), leftover)
	}
}
