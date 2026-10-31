package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0102(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV094AndV097(t *testing.T) {
	s := docStrV0102(t, "PLAN.md")
	if !strings.Contains(s, "v0.9.4") || !strings.Contains(s, "v0.9.7") {
		t.Error("PLAN.md v0.9.4·v0.9.7 마일스톤 누락")
	}
}

func TestSkillDocumentsReadFlags(t *testing.T) {
	s := docStrV0102(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(s, "--status") || !strings.Contains(s, "--kind") {
		t.Error("SKILL.md read 플래그 미문서화")
	}
}
