package doclint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func activePlans(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../../docs/plans")
	if err != nil {
		t.Fatalf("docs/plans 탐색 실패: %v", err)
	}
	var plans []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" || strings.HasSuffix(name, ".report.md") {
			continue
		}
		plans = append(plans, name)
	}
	return plans
}

func TestPlansIndexInSync(t *testing.T) {
	idx, err := os.ReadFile("../../docs/plans/README.md")
	if err != nil {
		t.Fatalf("docs/plans/README.md(활성 색인) 없음: %v", err)
	}
	s := string(idx)
	for _, p := range activePlans(t) {
		if !strings.Contains(s, p) {
			t.Errorf("활성 플랜 %q이(가) 색인에 없음 — docs/plans/README.md 표에 행 추가", p)
		}
	}
	rowRe := regexp.MustCompile("`([0-9]{4}-[0-9]{2}-[0-9]{2}-[^`]+\\.md)`")
	for _, m := range rowRe.FindAllStringSubmatch(s, -1) {
		if _, err := os.Stat(filepath.Join("../../docs/plans", m[1])); err != nil {
			t.Errorf("색인 행 %q이(가) 실재 파일을 가리키지 않음 — archive 이동 누락 또는 오타", m[1])
		}
	}
}

func TestActivePlansHaveStatusHeader(t *testing.T) {

	statusRe := regexp.MustCompile(`(?m)^상태: *(draft|active|blocked|approved.*) *$`)
	for _, p := range activePlans(t) {
		b, err := os.ReadFile(filepath.Join("../../docs/plans", p))
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", p, err)
		}
		if !statusRe.MatchString(string(b)) {
			t.Errorf("%s 머리에 '상태: draft|active|blocked' 줄 없음 — 헤더 템플릿(plans/README.md) 적용", p)
		}
	}
}
