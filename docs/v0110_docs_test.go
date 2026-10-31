package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0110(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillMentionsHotFull(t *testing.T) {
	if !strings.Contains(docStrV0110(t, "../flint-plugin/skills/flint/SKILL.md"), "hot --full") {
		t.Error("SKILL.md 펼침 안내 누락")
	}
}

func TestReadmeMentionsExposure(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0110(t, p), "exposure") {
			t.Errorf("%s 프로파일 미언급", p)
		}
	}
}

func TestPlanRecordsV0110(t *testing.T) {
	if !strings.Contains(docStrV0110(t, "PLAN.md"), "v0.11.0") {
		t.Error("PLAN.md v0.11.0 마일스톤 누락")
	}
}
