package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0160(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0160(t *testing.T) {
	if !strings.Contains(docStrV0160(t, "PLAN.md"), "v0.16.0") {
		t.Error("PLAN.md v0.16.0 마일스톤 누락")
	}
}

func TestBadgeIsV0160(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0160(t, p), "release-v0.16.0") {
			t.Errorf("%s 배지 v0.16.0 미갱신", p)
		}
	}
}

func TestReadmePluginInstall(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0160(t, p), "marketplace add synuns/flint") {
			t.Errorf("%s 플러그인 설치 안내 누락", p)
		}
	}
}
