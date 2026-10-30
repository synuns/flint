package docs

import (
	"os"
	"strings"
	"testing"
)

func readmeStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeBadgeIsV093(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(readmeStr(t, p), "release-v0.10.0") {
			t.Errorf("%s 배지가 v0.9.7 아님", p)
		}
	}
}

func TestReadmeEmbedsDiagrams(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := readmeStr(t, p)
		if !strings.Contains(s, "assets/flint-architecture.svg") || !strings.Contains(s, "assets/flint-lifecycle.svg") {
			t.Errorf("%s 다이어그램 임베드 누락", p)
		}
	}
}

func TestPlanRecordsV092AndV093(t *testing.T) {
	s := readmeStr(t, "PLAN.md")
	if !strings.Contains(s, "v0.9.2") || !strings.Contains(s, "v0.9.3") {
		t.Error("PLAN.md 마일스톤 v0.9.2·v0.9.3 기록 누락")
	}
}
