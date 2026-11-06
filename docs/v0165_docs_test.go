package docs

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestServerJsonValid(t *testing.T) {
	b, err := os.ReadFile("../mcp-registry/server.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "io.github.synuns/flint" {
		t.Errorf("name=%v", m["name"])
	}
	if d, _ := m["description"].(string); len(d) > 100 {
		t.Errorf("description %d자, want ≤100", len(d))
	}
}

func TestReadmeSetupMcp(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), "setup --mcp") {
			t.Errorf("%s setup --mcp 미기록", p)
		}
	}
}

// TestBadgeIsV0165 — v0.16.6 전환으로 삭제(선례).
