package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0130(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0130(t *testing.T) {
	if !strings.Contains(docStrV0130(t, "PLAN.md"), "v0.13.0") {
		t.Error("PLAN.md v0.13.0 마일스톤 누락")
	}
}

func TestTuningRecordsConflictThreshold(t *testing.T) {
	s := docStrV0130(t, "TUNING.md")
	if !strings.Contains(s, "conflictDupThreshold") || !strings.Contains(s, "0.6") {
		t.Error("TUNING.md 모순 임계값 누락")
	}
}

func TestReadmeMentionsAdoption(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStrV0130(t, p)
		if !strings.Contains(s, "read_count") && !strings.Contains(s, "채택") && !strings.Contains(s, "adoption") {
			t.Errorf("%s 채택 재랭킹 미언급", p)
		}
	}
}
