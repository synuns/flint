package doclint

import (
	"strings"
	"testing"
)

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
		t.Error("adapters/opencode.md에 mcpServers 있음 — 금지, opencode는 mcp 키(C4a)")
	}
}

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
