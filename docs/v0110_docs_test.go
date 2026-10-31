package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0110(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillMentionsHotFull(t *testing.T) {
	if !strings.Contains(docStrV0110(t, "../flint-plugin/skills/flint/SKILL.md"), "hot --full") {
		t.Error("SKILL.md 펼침 안내 누락")
	}
}

func TestReadmeMentionsExposure(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0110(t, p), "exposure") {
			t.Errorf("%s 프로파일 미언급", p)
		}
	}
}

func TestPlanRecordsV0110(t *testing.T) {
	if !strings.Contains(docStrV0110(t, "PLAN.md"), "v0.11.0") {
		t.Error("PLAN.md v0.11.0 마일스톤 누락")
	}
}

// TestSkillHookCommandsMatchR4 — SKILL.md 훅 설명이 v0.11.0 R4 이후
// 실제 hooks.json 커맨드(session-start·session-end)와 정합해야 한다.
// 낡은 직접 호출 표기("Stop 훅은 `flint nudge`")가 부활하면 실패.
func TestSkillHookCommandsMatchR4(t *testing.T) {
	skill := docStrV0110(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(skill, "session-end") {
		t.Error("SKILL.md Stop 훅 session-end 커맨드 미언급")
	}
	if strings.Contains(skill, "Stop 훅은 `flint nudge`") {
		t.Error("SKILL.md 낡은 Stop 훅 표기(flint nudge 직접 호출)")
	}
}

// TestReadmeHookEntriesUseR4Commands — README(EN/KO) 훅 3엔트리 괄호
// 표기가 실제 설치 커맨드(session-start·compact·session-end)와 일치.
func TestReadmeHookEntriesUseR4Commands(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		doc := docStrV0110(t, p)
		for _, cmd := range []string{"session-start", "compact", "session-end"} {
			if !strings.Contains(doc, cmd) {
				t.Errorf("%s 훅 엔트리 커맨드 %s 미언급", p, cmd)
			}
		}
		if strings.Contains(doc, "SessionStart (`hot`)") {
			t.Errorf("%s 낡은 SessionStart 표기(hot 직접 호출)", p)
		}
	}
}

// TestPlanV0110MilestonesChecked — R1~R5 전 구현 완료 시점부터 v0.11.0
// 마일스톤 섹션에 미완료 체크박스가 남으면 실패(관례: 완료 항목 [x]).
func TestPlanV0110MilestonesChecked(t *testing.T) {
	plan := docStrV0110(t, "PLAN.md")
	start := strings.Index(plan, "v0.11.0")
	end := strings.Index(plan[start:], "\n## 15.")
	if start < 0 || end < 0 {
		t.Fatal("PLAN.md v0.11.0 섹션 경계 탐색 실패")
	}
	section := plan[start : start+end]
	if strings.Contains(section, "- [ ]") {
		t.Error("PLAN.md v0.11.0 섹션에 미완료 체크박스 잔존")
	}
	want := "- [x] R5 문서 동기화"
	if !strings.Contains(section, want) {
		t.Errorf("PLAN.md v0.11.0 R5 완료 표기(%q) 누락", want)
	}
}
