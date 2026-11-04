package flintplugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readV0161(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMcpUsesPathCommand(t *testing.T) {
	m := readV0161(t, ".mcp.json")
	srv, _ := m["mcpServers"].(map[string]any)["flint"].(map[string]any)
	if srv == nil || srv["command"] != "flint" {
		t.Errorf("mcp command=%v, want flint(PATH)", srv)
	}
}

func TestPluginVersionV0161(t *testing.T) {
	p := readV0161(t, ".claude-plugin/plugin.json")
	v, _ := p["version"].(string)
	if !strings.HasPrefix(v, "0.16.1") {
		t.Errorf("plugin.json version=%q, want 0.16.1", v)
	}
}
