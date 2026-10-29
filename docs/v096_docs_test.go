package docs

import (
	"os"
	"strings"
	"testing"
)

func docStr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadmeMentionsFallback(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "fallback") {
			t.Errorf("%s read 폴백 미언급", p)
		}
	}
}

func TestPlanRecordsV095AndV096(t *testing.T) {
	s := docStr(t, "PLAN.md")
	if !strings.Contains(s, "v0.9.5") || !strings.Contains(s, "v0.9.6") {
		t.Error("PLAN.md v0.9.5·v0.9.6 마일스톤 누락")
	}
}
