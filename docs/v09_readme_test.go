// v09_readme_test.go — T5 README 지원 현황 검증. v0.9 하니스 5종
// (스펙 §1)이 두 README의 에이전트·어댑터 표에 링크로 반영됐고, 그
// 링크가 실재 파일을 가리키는지 검증한다. 기존 v06/v09 문서 테스트
// 패턴(read 헬퍼·grep 정합)을 승계한다.
package docs

import (
	"os"
	"strings"
	"testing"
)

// TestV09ReadmeAdapterTable — 두 README 모두 어댑터 5종(codex·gemini·
// copilot·opencode·cursor) 표 행 링크 포함 + 링크 대상 파일 실재.
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
			if _, err := os.Stat("../" + link); err != nil {
				t.Errorf("%s 링크가 실재하지 않는 파일을 가리킴: %s", readme, link)
			}
		}
	}
}
