package mcp

import (
	"strings"
	"testing"
)

func TestDispatchWriteRejectsBadKind(t *testing.T) {
	calls, _ := installRun(t, `{"status":"committed","id":1}`, 0)

	out, code := DispatchTool("flint_write", map[string]any{
		"kind": "bogus", "payload": map[string]any{"topic": "t"},
	})
	if code != 3 {
		t.Errorf("bogus kind code = %d, want 3", code)
	}
	if !strings.Contains(out, `"error":"schema"`) || !strings.Contains(out, "kind") {
		t.Errorf("bogus kind out = %q, want schema/kind error", out)
	}
	if len(*calls) != 0 {
		t.Errorf("bogus kind reached runFn: %v", (*calls)[0].args)
	}

	for _, kind := range []string{"knowledge", "session", "project"} {
		DispatchTool("flint_write", map[string]any{
			"kind": kind, "payload": map[string]any{},
		})
	}
	if len(*calls) != 3 {
		t.Fatalf("valid kinds calls = %d, want 3", len(*calls))
	}
	for i, kind := range []string{"knowledge", "session", "project"} {
		if got := (*calls)[i].args; !equal(got, []string{"write", kind, "--source", "mcp"}) {
			t.Errorf("kind %q args = %v", kind, got)
		}
	}
}

func TestDispatchHotNoSessionResidue(t *testing.T) {
	calls, _ := installRun(t, "hot summary", 0)
	_, code := DispatchTool("flint_hot", map[string]any{"session_id": "s1"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	wantArgs(t, calls, []string{"hot"})
}
