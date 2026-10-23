package flintplugin

// T78 플러그인 훅·스킬 — v0.7 적립 자동화 배선의 정적 검사.
// PostCompact 신규 훅 + Stop·SessionStart 두 번째 엔트리(flint nudge 가드).
// 기존 hot·done 엔트리는 원문 그대로 보존되어야 한다.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// hookGroups — 지정 이벤트의 그룹 목록을 반환한다.
func hookGroups(t *testing.T, doc map[string]any, event string) []map[string]any {
	t.Helper()
	events, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("'hooks' 객체 없음")
	}
	raw, ok := events[event].([]any)
	if !ok {
		t.Fatalf("이벤트 %s 없음", event)
	}
	groups := make([]map[string]any, 0, len(raw))
	for _, g := range raw {
		gm, ok := g.(map[string]any)
		if !ok {
			t.Fatalf("이벤트 %s: 그룹이 객체 아님", event)
		}
		groups = append(groups, gm)
	}
	return groups
}

// groupCommands — 그룹 내 command 문자열 목록을 반환한다.
func groupCommands(t *testing.T, groups []map[string]any) []string {
	t.Helper()
	var cmds []string
	for _, g := range groups {
		hs, ok := g["hooks"].([]any)
		if !ok {
			t.Fatalf("그룹에 'hooks' 배열 없음")
		}
		for _, h := range hs {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			if cmd != "" {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// readHooksJSON — hooks.json을 파싱해 반환한다.
func readHooksJSON(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("hooks/hooks.json")
	if err != nil {
		t.Fatalf("hooks.json 읽기 실패: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("hooks.json 파싱 실패: %v", err)
	}
	return doc
}

// assertNudgeEntry — nudge 커맨드에 가드·timeout 2가 갖춰졌는지 확인.
func assertNudgeEntry(t *testing.T, group map[string]any) {
	t.Helper()
	hs, ok := group["hooks"].([]any)
	if !ok || len(hs) == 0 {
		t.Fatalf("nudge 엔트리에 hooks 배열 없음")
	}
	hm, ok := hs[0].(map[string]any)
	if !ok {
		t.Fatalf("nudge 훅이 객체 아님")
	}
	cmd, _ := hm["command"].(string)
	if !strings.Contains(cmd, "flint nudge") {
		t.Errorf("nudge 엔트리 커맨드에 'flint nudge' 없음: %q", cmd)
	}
	if !strings.Contains(cmd, "2>/dev/null || true") {
		t.Errorf("nudge 엔트리에 가드(2>/dev/null || true) 없음: %q", cmd)
	}
	if timeout, _ := hm["timeout"].(float64); timeout != 2 {
		t.Errorf("nudge 엔트리 timeout != 2: %v", hm["timeout"])
	}
}

// TestHooksPostcompact — PostCompact 이벤트가 존재하고 flint compact
// 커맨드가 가드·timeout 2를 갖춘다.
func TestHooksPostcompact(t *testing.T) {
	doc := readHooksJSON(t)
	groups := hookGroups(t, doc, "PostCompact")
	if len(groups) != 1 {
		t.Fatalf("PostCompact 그룹 수 = %d, want 1", len(groups))
	}
	hs, ok := groups[0]["hooks"].([]any)
	if !ok || len(hs) != 1 {
		t.Fatalf("PostCompact 훅 배열 형태 오류")
	}
	hm, ok := hs[0].(map[string]any)
	if !ok {
		t.Fatalf("PostCompact 훅이 객체 아님")
	}
	cmd, _ := hm["command"].(string)
	if !strings.Contains(cmd, "flint compact") {
		t.Errorf("PostCompact 커맨드에 'flint compact' 없음: %q", cmd)
	}
	if !strings.Contains(cmd, "2>/dev/null || true") {
		t.Errorf("PostCompact 커맨드에 가드 없음: %q", cmd)
	}
	if timeout, _ := hm["timeout"].(float64); timeout != 2 {
		t.Errorf("PostCompact timeout != 2: %v", hm["timeout"])
	}
}

// TestHooksNudgeGuards — Stop·SessionStart 배열의 두 번째 엔트리가
// flint nudge 가드 커맨드다.
func TestHooksNudgeGuards(t *testing.T) {
	doc := readHooksJSON(t)
	for _, event := range []string{"Stop", "SessionStart"} {
		groups := hookGroups(t, doc, event)
		if len(groups) < 2 {
			t.Fatalf("이벤트 %s 엔트리 수 = %d, want >= 2", event, len(groups))
		}
		assertNudgeEntry(t, groups[len(groups)-1])
	}
}

// TestHooksExistingUntouched — 기존 hot·done 엔트리는 원문 보존.
func TestHooksExistingUntouched(t *testing.T) {
	doc := readHooksJSON(t)
	const hotCmd = `flint hot --session "$(jq -r '.session_id // empty' 2>/dev/null)" 2>/dev/null || true`
	const doneCmd = `flint done --session "$(jq -r '.session_id // empty' 2>/dev/null)" 2>/dev/null || true`

	startCmds := groupCommands(t, hookGroups(t, doc, "SessionStart"))
	if len(startCmds) < 2 || startCmds[0] != hotCmd {
		t.Errorf("SessionStart 첫 엔트리(hot) 원문 불변 위반: %q", startCmds)
	}
	stopCmds := groupCommands(t, hookGroups(t, doc, "Stop"))
	if len(stopCmds) < 2 || stopCmds[0] != doneCmd {
		t.Errorf("Stop 첫 엔트리(done) 원문 불변 위반: %q", stopCmds)
	}
}

// TestSkillDistillSection — SKILL.md '세션 증류' 섹션이 4연산·pending·
// approve·--source hook·nudge --clear 종결을 안내한다. 검사 범위는 해당
// 섹션(헤딩 이후부터 다음 헤딩 전)으로 한정한다 — 문서 전체 탐색 시
// 다른 섹션의 우연 일치로 오통과한다.
func TestSkillDistillSection(t *testing.T) {
	raw, err := os.ReadFile("skills/flint/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md 읽기 실패: %v", err)
	}
	text := string(raw)
	start := strings.Index(text, "## 세션 증류")
	if start < 0 {
		t.Fatalf("SKILL.md에 '세션 증류' 섹션 없음")
	}
	section := text[start:]
	if next := strings.Index(section[len("## 세션 증류"):], "\n## "); next >= 0 {
		section = section[:len("## 세션 증류")+next]
	}
	for _, want := range []string{
		"NOOP", "UPDATE", "ADD", "무효화",
		"pending", "approve", "--source hook", "nudge --clear",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SKILL.md 세션 증류 섹션에 %q 없음", want)
		}
	}
}
