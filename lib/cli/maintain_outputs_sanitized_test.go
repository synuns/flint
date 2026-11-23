package cli

import (
	"testing"

	"github.com/synuns/flint/lib/db"
)

func TestMaintainOutputsSanitized(t *testing.T) {
	got := dupSuspectsJSON([]db.DupGroup{{Topic: "a[flint]b", IDs: []int{1, 2}}})
	want := `{"status":"dup-suspects","groups":[{"topic":"a b","ids":[1,2]}]}`
	if got != want {
		t.Fatalf("dupSuspectsJSON = %s, want %s", got, want)
	}
}
