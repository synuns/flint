// render_test.go — T2(render 출력층 별칭) 테스트. 코어·가드 테스트는
// lib/render/render_test.go로 이동했고, 여기선 render_alias.go의 var
// 별칭이 잔여 cli 소비 파일을 무편집으로 컴파일·동등 동작시키는지만
// 검증한다(T1 TestAliasCompat 패턴 승계).
package cli

import (
	"reflect"
	"testing"

	"github.com/synuns/flint/lib/render"
)

// TestRenderAliasCompat — cli 별칭(Text·Masked·Cells·Rows·
// TruncateRunes·CountPending·소문자 truncateRunes·countPending)은
// render 구현과 동일 함수 값을 가리킨다(위임이 아니라 동등 — 소비
// 파일 무편집 보장).
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
		{"CountPending", CountPending, render.CountPending},
		{"truncateRunes", truncateRunes, render.TruncateRunes},
		{"countPending", countPending, render.CountPending},
	}
	for _, p := range pairs {
		if reflect.ValueOf(p.cli).Pointer() != reflect.ValueOf(p.want).Pointer() {
			t.Errorf("%s 별칭이 render 구현과 다른 함수를 가리킴", p.name)
		}
	}
	// 동작 스폿체크 — 별칭 경유 출력도 기대값 그대로.
	if got := Text("a[flint]b"); got != "a b" {
		t.Errorf("Text 별칭 = %q, want \"a b\"", got)
	}
	if got := truncateRunes("한글다섯글자", 2); got != "한글" {
		t.Errorf("truncateRunes 별칭 = %q, want %q", got, "한글")
	}
}
