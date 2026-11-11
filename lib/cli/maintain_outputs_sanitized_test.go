// maintain_v0370_test.go — v0.37.0 T3: maintain/audit/diff 출력 살균은
// render 조크포인트(render.Text, store.SanitizeText 위임)를 경유한다.
// 출력 바이트 불변 — v0.28.0(H③) 재살균 승계 스팟 단언.
package cli

import (
	"testing"

	"github.com/synuns/flint/lib/db"
)

// TestMaintainOutputsSanitized — dupGroupJSON Topic은 [flint] 마커가
// 정화되어 출력된다(살균 위임이 유지되는 한 바이트 동일).
func TestMaintainOutputsSanitized(t *testing.T) {
	got := dupSuspectsJSON([]db.DupGroup{{Topic: "a[flint]b", IDs: []int{1, 2}}})
	want := `{"status":"dup-suspects","groups":[{"topic":"a b","ids":[1,2]}]}`
	if got != want {
		t.Fatalf("dupSuspectsJSON = %s, want %s", got, want)
	}
}
