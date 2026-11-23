package flintplugin

import "testing"

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
