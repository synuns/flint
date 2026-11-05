package flintplugin

import (
	"testing"
)

func TestMcpUsesPathCommand(t *testing.T) {
	m := readDoc(t, ".mcp.json")
	srv, _ := m["mcpServers"].(map[string]any)["flint"].(map[string]any)
	if srv == nil || srv["command"] != "flint" {
		t.Errorf("mcp command=%v, want flint(PATH)", srv)
	}
}

// v0.16.2: TestPluginVersionV0161 삭제 — plugin_v0162_test.go가 대체.
