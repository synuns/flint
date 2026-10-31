// plugin_v0132_test.go — v0.13.2 심층 리뷰 회귀(N6): 통합 세션 훅 3종의
// timeout이 10이다. 2초 상향 전 값으로 되돌아가면 DB 지연 시 주입이
// 통째로 소실된다(N6 진단).
package flintplugin

import "testing"

// TestHooksTimeout10AllEvents — SessionStart·Stop·PostCompact 세 이벤트
// 모든 커맨드 엔트리의 timeout이 10(양쪽 정합: hooks.json).
func TestHooksTimeout10AllEvents(t *testing.T) {
	doc := readHooksJSON(t)
	for _, event := range []string{"SessionStart", "Stop", "PostCompact"} {
		for _, g := range hookGroups(t, doc, event) {
			hs, ok := g["hooks"].([]any)
			if !ok {
				t.Fatalf("이벤트 %s 그룹에 hooks 배열 없음", event)
			}
			for _, h := range hs {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if timeout, _ := hm["timeout"].(float64); timeout != 10 {
					t.Errorf("%s timeout = %v, want 10", event, hm["timeout"])
				}
			}
		}
	}
}
