// maintain_v0370_test.go — the maintain/audit/diff output sanitization goes through
// the render choke point (render.Text, delegating to store.SanitizeText).
// Output bytes unchanged — asserting the re-sanitization hand-off spot.
package cli

import (
	"testing"

	"github.com/synuns/flint/lib/db"
)

// TestMaintainOutputsSanitized — dupGroupJSON's Topic is output with the [flint] marker
// neutralized (byte-identical as long as the sanitization delegation is kept).
func TestMaintainOutputsSanitized(t *testing.T) {
	got := dupSuspectsJSON([]db.DupGroup{{Topic: "a[flint]b", IDs: []int{1, 2}}})
	want := `{"status":"dup-suspects","groups":[{"topic":"a b","ids":[1,2]}]}`
	if got != want {
		t.Fatalf("dupSuspectsJSON = %s, want %s", got, want)
	}
}
