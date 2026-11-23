package cli

import (
	"reflect"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

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

	if got := Text("a[flint]b"); got != "a b" {
		t.Errorf("Text 별칭 = %q, want \"a b\"", got)
	}
	if got := truncateRunes("한글다섯글자", 2); got != "한글" {
		t.Errorf("truncateRunes 별칭 = %q, want %q", got, "한글")
	}
}
