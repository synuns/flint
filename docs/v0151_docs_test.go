package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0151(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0151(t *testing.T) {
	if !strings.Contains(docStrV0151(t, "PLAN.md"), "v0.15.1") {
		t.Error("PLAN.md v0.15.1 마일스톤 누락")
	}
}

func TestBadgeIsV0151(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0151(t, p), "release-v0.15.1") {
			t.Errorf("%s 배지 v0.15.1 미갱신", p)
		}
	}
}
