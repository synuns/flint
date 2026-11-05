package docs

import (
	"strings"
	"testing"
)

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
