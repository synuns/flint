package flintplugin

import (
	"strings"
	"testing"
)

func TestMcpUsesPathCommand(t *testing.T) {
	m := readDoc(t, ".mcp.json")
	srv, _ := m["mcpServers"].(map[string]any)["flint"].(map[string]any)
	if srv == nil || srv["command"] != "flint" {
		t.Errorf("mcp command=%v, want flint(PATH)", srv)
	}
}

func TestPluginVersionV0161(t *testing.T) {
	p := readDoc(t, ".claude-plugin/plugin.json")
	v, _ := p["version"].(string)
	if !strings.HasPrefix(v, "0.16.1") {
		t.Errorf("plugin.json version=%q, want 0.16.1", v)
	}
}
