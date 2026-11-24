package doclint

import (
	"strings"
	"testing"
)

func TestDocsSyncMinimalInjection(t *testing.T) {
	plan := docStr(t, "PLAN.md")
	i := strings.Index(plan, "## 4.")
	j := strings.Index(plan, "## 5.")
	if i < 0 || j < 0 || j <= i {
		t.Fatal("PLAN.md §4 원칙 절 구조 확인 실패")
	}
	planSection := plan[i:j]
	for _, want := range []string{
		"주입 최소 — 실증 원칙",
		"주간 rollup",
		"실시간 쿼리",
	} {
		if !strings.Contains(planSection, want) {
			t.Errorf("PLAN.md §4에 주입 최소 실증 원칙 문구 행 없음: %q", want)
		}
	}

	readme := docStr(t, "../README.md")
	for _, want := range []string{
		"Minimal injection — evidence principle",
		"weekly rollup",
		"no new live queries",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md에 minimal injection evidence 원칙 행 없음: %q", want)
		}
	}
}

func TestReadmeKoParity(t *testing.T) {
	ko := docStr(t, "../README.ko.md")
	for _, want := range []string{
		"주입 최소 — 실증 원칙",
		"주간 rollup",
		"실시간 쿼리",
	} {
		if !strings.Contains(ko, want) {
			t.Errorf("README.ko.md에 한국어 병기 행 없음: %q", want)
		}
	}
}
