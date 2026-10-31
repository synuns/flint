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
