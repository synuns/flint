package doclint

import (
	"os"
	"strings"
	"testing"
)

func TestV09ReadmeAdapterTable(t *testing.T) {
	harnesses := []string{"codex", "gemini", "copilot", "opencode", "cursor"}
	for _, readme := range []string{"../README.md", "../README.ko.md"} {
		doc := read(t, readme)
		for _, h := range harnesses {
			link := "docs/adapters/" + h + ".md"
			if !strings.Contains(doc, "("+link+")") {
				t.Errorf("%s에 %s 표 행 링크 없음", readme, link)
				continue
			}
			if _, err := os.Stat(docPath(link)); err != nil {
				t.Errorf("%s 링크가 실재하지 않는 파일을 가리킴: %s", readme, link)
			}
		}
	}
}
