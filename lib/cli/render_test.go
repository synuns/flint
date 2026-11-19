// render_test.go — tests for the render output-layer aliases. The core and guard tests
// moved to lib/render/render_test.go; here we only verify that the var aliases in
// render_alias.go compile and behave equivalently for the residual cli consumer files,
// unedited.
package cli

import (
	"reflect"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

// TestRenderAliasCompat — the cli aliases (Text, Masked, Cells, Rows,
// TruncateRunes, CountPending, and the lowercase truncateRunes and countPending) point to
// the same function values as the single implementation (equivalence, not delegation —
// guaranteeing consumer files stay unedited). CountPending and countPending use
// the knowledge implementation (moved to knowledge/pending.go).
func TestRenderAliasCompat(t *testing.T) {
	pairs := []struct {
		name string
		cli  any
		want any
	}{
		{"Text", Text, render.Text},
		{"Masked", Masked, render.Masked},
		{"Cells", Cells, render.Cells},
		{"Rows", Rows, render.Rows},
		{"TruncateRunes", TruncateRunes, render.TruncateRunes},
		{"CountPending", CountPending, knowledge.CountPending},
		{"truncateRunes", truncateRunes, render.TruncateRunes},
		{"countPending", countPending, knowledge.CountPending},
	}
	for _, p := range pairs {
		if reflect.ValueOf(p.cli).Pointer() != reflect.ValueOf(p.want).Pointer() {
			t.Errorf("%s 별칭이 render 구현과 다른 함수를 가리킴", p.name)
		}
	}
	// behavior spot-check — output via the aliases still matches the expected values.
	if got := Text("a[flint]b"); got != "a b" {
		t.Errorf("Text 별칭 = %q, want \"a b\"", got)
	}
	if got := truncateRunes("한글다섯글자", 2); got != "한글" {
		t.Errorf("truncateRunes 별칭 = %q, want %q", got, "한글")
	}
}
