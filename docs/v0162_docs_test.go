package docs

import (
	"strings"
	"testing"
)

func TestSkillWriteProtocol(t *testing.T) {
	b := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	for _, want := range []string{"갱신 우선", "최소화", "flint read"} {
		if !strings.Contains(b, want) {
			t.Errorf("SKILL.md 쓰기 프로토콜 누락(%s)", want)
		}
	}
}

func TestPlanRecordsV0162(t *testing.T) {
	if !strings.Contains(docStr(t, "PLAN.md"), "v0.16.2") {
		t.Error("PLAN.md v0.16.2 마일스톤 누락")
	}
}

func TestBadgeIsV0162(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "release-v0.16.2") {
			t.Errorf("%s 배지 v0.16.2 미갱신", p)
		}
	}
}
