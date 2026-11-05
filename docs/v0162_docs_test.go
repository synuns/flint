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

// TestBadgeIsV0162 — v0.16.3 배지 범프로 삭제(선례: 26bdf0e 버전 동기
// 선례 삭제 룰링. 대체 가드는 v0163_docs_test.go·plannedBadgeVersions).
