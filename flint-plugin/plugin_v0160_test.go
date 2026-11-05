package flintplugin

import (
	"testing"
)

// v0.16.1: TestPluginManifestSync 삭제 — 0.16.0 prefix 강제(버전 동기).
// TestPluginVersionV0161(plugin_v0161_test.go)이 대체. name 검증은
// validate_yaml.py가 담당.

func TestMarketplaceStructure(t *testing.T) {
	m := readDoc(t, "../.claude-plugin/marketplace.json")
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
