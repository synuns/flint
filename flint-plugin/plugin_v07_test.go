package flintplugin

// T78 플러그인 훅·스킬 — v0.7 적립 자동화 배선의 정적 검사.
// PostCompact 신규 훅 + 세션 훅 통합 엔트리(v0.11.0 R4: session-start·
// session-end가 nudge 시퀀스를 흡수, 가드·timeout 승계).
// 통합 엔트리 커맨드는 원문 그대로 보존되어야 한다.

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

// assertNudgeEntry — 세션 훅 커맨드에 가드·timeout 10이 갖춰졌는지 확인.
// v0.11.0 R4 통합: nudge는 session-start·session-end 내부 시퀀스로 흡수
// 됐다 — 가드·타임아웃 단언은 통합 커맨드 대상으로 승계한다.
func assertNudgeEntry(t *testing.T, group map[string]any, sub string) {
	t.Helper()
	hs, ok := group["hooks"].([]any)
	if !ok || len(hs) == 0 {
		t.Fatalf("세션 훅 엔트리에 hooks 배열 없음")
	}
	hm, ok := hs[0].(map[string]any)
	if !ok {
		t.Fatalf("세션 훅이 객체 아님")
	}
	cmd, _ := hm["command"].(string)
	if !strings.Contains(cmd, "flint "+sub) {
		t.Errorf("통합 엔트리 커맨드에 'flint %s' 없음: %q", sub, cmd)
	}
	if !strings.Contains(cmd, "2>/dev/null || true") {
		t.Errorf("통합 엔트리에 가드(2>/dev/null || true) 없음: %q", cmd)
	}
	if timeout, _ := hm["timeout"].(float64); timeout != 10 {
		t.Errorf("통합 엔트리 timeout != 10: %v", hm["timeout"])
	}
}

// TestHooksPostcompact — PostCompact 이벤트가 존재하고 flint compact
// 커맨드가 가드·timeout 10을 갖춘다.
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
	if timeout, _ := hm["timeout"].(float64); timeout != 10 {
		t.Errorf("PostCompact timeout != 10: %v", hm["timeout"])
	}
}

// TestHooksNudgeGuards — SessionStart·Stop 배열의 통합 엔트리
// (session-start·session-end)가 가드·timeout 10을 갖춘다.
func TestHooksNudgeGuards(t *testing.T) {
	doc := readHooksJSON(t)
	assertNudgeEntry(t, hookGroups(t, doc, "SessionStart")[0], "session-start")
	assertNudgeEntry(t, hookGroups(t, doc, "Stop")[0], "session-end")
}

// TestHooksExistingUntouched — 세션 훅 통합 엔트리는 원문 보존.
func TestHooksExistingUntouched(t *testing.T) {
	doc := readHooksJSON(t)
	const startCmd = `flint session-start --session "$(jq -r '.session_id // empty' 2>/dev/null)" 2>/dev/null || true`
	const endCmd = `flint session-end --session "$(jq -r '.session_id // empty' 2>/dev/null)" 2>/dev/null || true`

	startCmds := groupCommands(t, hookGroups(t, doc, "SessionStart"))
	if len(startCmds) < 1 || startCmds[0] != startCmd {
		t.Errorf("SessionStart 첫 엔트리(session-start) 원문 불변 위반: %q", startCmds)
	}
	stopCmds := groupCommands(t, hookGroups(t, doc, "Stop"))
	if len(stopCmds) < 1 || stopCmds[0] != endCmd {
		t.Errorf("Stop 첫 엔트리(session-end) 원문 불변 위반: %q", stopCmds)
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
