package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0102(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV094AndV097(t *testing.T) {
	s := docStrV0102(t, "PLAN.md")
	if !strings.Contains(s, "v0.9.4") || !strings.Contains(s, "v0.9.7") {
		t.Error("PLAN.md v0.9.4·v0.9.7 마일스톤 누락")
	}
}

func TestSkillDocumentsReadFlags(t *testing.T) {
	s := docStrV0102(t, "../flint-plugin/skills/flint/SKILL.md")
	for _, f := range []string{"--tag", "--entity", "--domain", "--limit"} {
		if !strings.Contains(s, f) {
			t.Error("SKILL.md read 플래그 미문서화: " + f)
		}
	}
}

// TestSkillReadLineDocumentsOnlyRealFlags — read는 runRead(lib/cli/read.go)가
// 받는 플래그만 문서화해야 한다. --status·--kind는 파싱되지 않는 미구현
// 플래그라 read 줄에 등장하면 안 된다(pending 조회는 `flint hot` 팔).
func TestSkillReadLineDocumentsOnlyRealFlags(t *testing.T) {
	s := docStrV0102(t, "../flint-plugin/skills/flint/SKILL.md")
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "`flint read ") {
			for _, ghost := range []string{"--status", "--kind"} {
				if strings.Contains(line, ghost) {
					t.Errorf("SKILL.md read 줄이 미구현 플래그 문서화: %s — %s", ghost, line)
				}
			}
		}
	}
	if !strings.Contains(s, "pending — 대기 승격 대상 조회") {
		t.Error("SKILL.md 대기 승격 대상 조회(hot pending) 안내 누락")
	}
}
