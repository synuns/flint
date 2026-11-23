package flintplugin

import (
	"testing"
)

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
