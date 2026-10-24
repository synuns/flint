// v09_docs_b_test.go — T4 어댑터 문서 계약 검증. v0.9 하니스 5종 중
// opencode·cursor 2종(스펙 §2 C4~C5) 문서가 존재하고, 설정 스니펫 키·
// 지시파일·session agent 필드 안내를 정확한 포맷으로 담는지 grep 정합
// 검증한다. codex·gemini·copilot은 v09_docs_test.go(T3 소유) 참조.
package docs

import (
	"strings"
	"testing"
)

// TestV09AdapterDocsOpencode — C4: opencode.json "mcp" 키(type local,
// command 배열 — C4a), 지시파일 AGENTS.md. mcpServers 키 사용 불가.
func TestV09AdapterDocsOpencode(t *testing.T) {
	doc := read(t, "adapters/opencode.md")
	for _, want := range []string{
		"~/.config/opencode/opencode.json",
		`"mcp": {`,
		`"type": "local"`,
		`"command": ["flint", "mcp"]`,
		"AGENTS.md",
		`"agent"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("adapters/opencode.md에 %s 없음", want)
		}
	}
	if strings.Contains(doc, "mcpServers") {
		t.Error("adapters/opencode.md에 mcpServers 없음 — opencode는 mcp 키(C4a)")
	}
}

// TestV09AdapterDocsCursor — C5: .cursor/mcp.json mcpServers JSON(Claude
// 동일), 지시파일 AGENTS.md.
func TestV09AdapterDocsCursor(t *testing.T) {
	doc := read(t, "adapters/cursor.md")
	for _, want := range []string{
		".cursor/mcp.json",
		"~/.cursor/mcp.json",
		"mcpServers",
		`"args": ["mcp"]`,
		"AGENTS.md",
		`"agent"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("adapters/cursor.md에 %s 없음", want)
		}
	}
}
