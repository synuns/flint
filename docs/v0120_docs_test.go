package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0120(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeBadgeIsV0120(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0120(t, p), "release-v0.12.0") {
			t.Errorf("%s 배지가 v0.12.0 아님", p)
		}
	}
}

func TestSkillDocumentsReadStatus(t *testing.T) {
	s := docStrV0120(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(s, "read --status pending") {
		t.Error("SKILL.md pending 열람 미안내")
	}
}

func TestPlanRecordsV0120(t *testing.T) {
	if !strings.Contains(docStrV0120(t, "PLAN.md"), "v0.12.0") {
		t.Error("PLAN.md v0.12.0 마일스톤 누락")
	}
}
