package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0161(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0161(t *testing.T) {
	if !strings.Contains(docStrV0161(t, "PLAN.md"), "v0.16.1") {
		t.Error("PLAN.md v0.16.1 마일스톤 누락")
	}
}

func TestBadgeIsV0161(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0161(t, p), "release-v0.16.1") {
			t.Errorf("%s 배지 v0.16.1 미갱신", p)
		}
	}
}

func TestReadmePathRequirement(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStrV0161(t, p)
		if !strings.Contains(s, "PATH") {
			t.Errorf("%s 바이너리 PATH 요구 미기록", p)
		}
	}
}
