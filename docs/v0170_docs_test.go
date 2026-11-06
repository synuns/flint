// v0170_docs_test.go — v0.17.0 의존성 관리·버전 일괄 범프 회귀.
package docs

import (
	"regexp"
	"strings"
	"testing"
)

// v0.17.1: TestVersion0170·TestBadgeV0170 삭제 — 버전 동기 가드는
// VERSION 파일 기반 TestVersionArtifactsSync(v0171_docs_test.go)로 범용화.

// TestDependabotConfigured — R1: gomod·github-actions 주간 업데이트 2 ecosystem.
func TestDependabotConfigured(t *testing.T) {
	doc := docStr(t, "../.github/dependabot.yml")
	for _, want := range []string{
		`package-ecosystem: "gomod"`,
		`package-ecosystem: "github-actions"`,
		`interval: "weekly"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf(".github/dependabot.yml에 %s 없음", want)
		}
	}
}

// TestMcpPublisherPinned — R1: CI mcp-publisher 다운로드가 latest 부유가
// 아닌 특정 태그로 고정됐는지(재현성).
func TestMcpPublisherPinned(t *testing.T) {
	ci := docStr(t, "../.github/workflows/ci.yml")
	re := regexp.MustCompile(`registry/releases/((?:latest|download/v[0-9.]+))/mcp-publisher`)
	m := re.FindStringSubmatch(ci)
	if m == nil {
		t.Fatal("ci.yml mcp-publisher 다운로드 URL 파싱 실패")
	}
	if m[1] == "latest" {
		t.Errorf("mcp-publisher URL이 버전 미고정(latest): %s", m[1])
	}
}
