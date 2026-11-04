package flintplugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readV0160(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s 파싱: %v", path, err)
	}
	return m
}

func TestPluginManifestSync(t *testing.T) {
	p := readV0160(t, ".claude-plugin/plugin.json")
	v, _ := p["version"].(string)
	if !strings.HasPrefix(v, "0.16.0") {
		t.Errorf("plugin.json version=%q, want 0.16.0 계열(배지 동기)", v)
	}
	if p["name"] != "flint" {
		t.Errorf("plugin.json name=%v, want flint", p["name"])
	}
}

func TestMarketplaceStructure(t *testing.T) {
	m := readV0160(t, "../.claude-plugin/marketplace.json")
	if m["name"] != "flint" {
		t.Errorf("marketplace name=%v, want flint", m["name"])
	}
	plugs, _ := m["plugins"].([]any)
	if len(plugs) != 1 {
		t.Fatalf("plugins %d건, want 1", len(plugs))
	}
	e, _ := plugs[0].(map[string]any)
	if e["source"] != "./flint-plugin" || e["name"] != "flint" {
		t.Errorf("plugins[0]=%v (source ./flint-plugin·name flint)", e)
	}
}
