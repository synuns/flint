package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0152(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0152(t *testing.T) {
	if !strings.Contains(docStrV0152(t, "PLAN.md"), "v0.15.2") {
		t.Error("PLAN.md v0.15.2 마일스톤 누락")
	}
}

func TestBadgeIsV0152(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0152(t, p), "release-v0.15.2") {
			t.Errorf("%s 배지 v0.15.2 미갱신", p)
		}
	}
}
