// tools_v1230_test.go — server-side whitelist validation of the
// dispatchWrite kind, and removal of the dispatchHot session_id residue.
package mcp

import (
	"strings"
	"testing"
)

// TestDispatchWriteRejectsBadKind — B5: an invalid kind is a schema error
// before reaching runFn (exit 3, the existing MCP error format). A valid
// kind (knowledge) passes through to the cli write assembly. The whitelist
// source is kindExpect in lib/store/validate.go
// (knowledge|session|project) — unexported, so kept in sync by value
// contract.
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

// TestDispatchHotNoSessionResidue — session_id is never assembled into
// --session (a query-neutral no-op).
func TestDispatchHotNoSessionResidue(t *testing.T) {
	calls, _ := installRun(t, "hot summary", 0)
	_, code := DispatchTool("flint_hot", map[string]any{"session_id": "s1"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	wantArgs(t, calls, []string{"hot"})
}
