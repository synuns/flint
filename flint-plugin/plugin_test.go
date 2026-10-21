package flintplugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// loadHooksJSON reads hooks/hooks.json into generic maps.
func loadHooksJSON(t *testing.T) map[string]any {
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

// hookCommands flattens every hook command string for the given event.
func hookCommands(t *testing.T, doc map[string]any, event string) []map[string]any {
	t.Helper()
	events, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("'hooks' 객체 없음")
	}
	list, ok := events[event].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("이벤트 %s 훅 없음", event)
	}
	var out []map[string]any
	for _, matcher := range list {
		m, ok := matcher.(map[string]any)
		if !ok {
			continue
		}
		hs, ok := m["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hs {
			if hm, ok := h.(map[string]any); ok {
				out = append(out, hm)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("이벤트 %s 실행 가능한 훅 없음", event)
	}
	return out
}

func TestHooksJSONValid(t *testing.T) {
	doc := loadHooksJSON(t)

	start := hookCommands(t, doc, "SessionStart")[0]
	cmd, _ := start["command"].(string)
	if !strings.Contains(cmd, "flint hot --session") {
		t.Errorf("SessionStart 커맨드에 'flint hot --session' 없음: %q", cmd)
	}
	if got := start["timeout"]; got != float64(2) {
		t.Errorf("SessionStart timeout = %v, want 2", got)
	}
	if !strings.Contains(cmd, "|| true") {
		t.Errorf("SessionStart 비차단 보장('|| true') 없음: %q", cmd)
	}

	stop := hookCommands(t, doc, "Stop")[0]
	stopCmd, _ := stop["command"].(string)
	if !strings.Contains(stopCmd, "flint done --session") {
		t.Errorf("Stop 커맨드에 'flint done --session' 없음: %q", stopCmd)
	}
	if got := stop["timeout"]; got != float64(2) {
		t.Errorf("Stop timeout = %v, want 2", got)
	}
	// Stop 훅은 세션 종료를 막지 않아야 한다: no-op(true) 또는 비차단.
	if !strings.Contains(stopCmd, "true") {
		t.Errorf("Stop v0.1 no-op 표시 없음: %q", stopCmd)
	}
}

// TestHookSessionIDFromStdin — Claude Code 훅에는 CLAUDE_SESSION_ID 환경변수가
// 없다. 세션 id는 stdin JSON의 session_id 필드로 전달되므로, 훅 커맨드는
// jq로 stdin을 파싱해야 하고 존재하지 않는 환경변수를 참조해선 안 된다.
func TestHookSessionIDFromStdin(t *testing.T) {
	raw, err := os.ReadFile("hooks/hooks.json")
	if err != nil {
		t.Fatalf("hooks.json 읽기 실패: %v", err)
	}
	if strings.Contains(string(raw), "CLAUDE_SESSION_ID") {
		t.Errorf("존재하지 않는 환경변수 CLAUDE_SESSION_ID 참조가 남아 있음")
	}

	doc := loadHooksJSON(t)
	for _, event := range []string{"SessionStart", "Stop"} {
		hook := hookCommands(t, doc, event)[0]
		cmd, _ := hook["command"].(string)
		if !strings.Contains(cmd, `jq -r '.session_id`) {
			t.Errorf("%s 커맨드가 stdin JSON의 session_id를 파싱하지 않음: %q", event, cmd)
		}
	}
}

// TestHookMatchers — SessionStart matcher는 시작 소스 정규식이며 "*"는
// 관례가 아니다. Stop은 matcher를 사용하지 않는다.
func TestHookMatchers(t *testing.T) {
	doc := loadHooksJSON(t)

	events := doc["hooks"].(map[string]any)
	startGroups, _ := events["SessionStart"].([]any)
	for _, g := range startGroups {
		group := g.(map[string]any)
		matcher, _ := group["matcher"].(string)
		if matcher == "*" || matcher == "" {
			t.Errorf("SessionStart matcher = %q, want 시작 소스 나열 (startup|resume|clear|compact)", matcher)
		}
	}

	stopGroups, _ := events["Stop"].([]any)
	for _, g := range stopGroups {
		group := g.(map[string]any)
		if _, ok := group["matcher"]; ok {
			t.Errorf("Stop 그룹에 matcher 키가 있음: %v (Stop은 matcher 미사용)", group["matcher"])
		}
	}
}

func TestPluginJSONValid(t *testing.T) {
	raw, err := os.ReadFile(".claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("plugin.json 읽기 실패: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("plugin.json 파싱 실패: %v", err)
	}
	if doc["name"] != "flint" {
		t.Errorf("plugin.json name = %v, want flint", doc["name"])
	}
}

func TestMCPJSONValid(t *testing.T) {
	raw, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf(".mcp.json 읽기 실패: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf(".mcp.json 파싱 실패: %v", err)
	}
	if _, ok := doc["mcpServers"]; !ok {
		t.Errorf(".mcp.json에 mcpServers 키 없음 (v0.3 예약 빈 정의)")
	}
}
