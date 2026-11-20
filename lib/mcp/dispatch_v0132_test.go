// dispatch_v0132_test.go — dispatchRead assembles the external-input query
// after "--" to shield it from flag parsing. The real-implementation seam
// (installSimple) allows a real DB only when FLINT_TEST_DSN is set (no
// hardcoding or connecting to the production default DSN).
package mcp

import (
	"testing"
)

// TestDispatchReadFlagLikeQueryGuard — N9: the query "--json" is placed
// after "--" in the CLI args and passed as a search term (flag-misparse
// prevention — output-mode invariance is covered by the lib/cli parser
// regression TestReadDashDashTreatsRestAsQuery).
func TestDispatchReadFlagLikeQueryGuard(t *testing.T) {
	calls, _ := installRun(t, "", 0)
	DispatchTool("flint_read", map[string]any{"query": "--json"})
	wantArgs(t, calls, []string{"read", "--source", "mcp", "--", "--json"})
}
