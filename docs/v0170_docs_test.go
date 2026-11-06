// v0170_docs_test.go — v0.17.0 의존성 관리·버전 일괄 범프 회귀.
package docs

import (
	"regexp"
	"strings"
	"testing"
)

// TestVersion0170 — VERSION 파일(단일 버전 소스, v0.16.7 도입)과 plugin.json이
// 모두 v0.17.0인지. scripts/bump_version.sh 일괄 범프 누락 회귀.
func TestVersion0170(t *testing.T) {
	if v := strings.TrimSpace(docStr(t, "../VERSION")); v != "0.17.0" {
		t.Errorf("VERSION=%q, want 0.17.0", v)
	}
	m := regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`).
		FindStringSubmatch(docStr(t, "../flint-plugin/.claude-plugin/plugin.json"))
	if m == nil || m[1] != "0.17.0" {
		t.Errorf("plugin.json version=%v, want 0.17.0", m)
	}
}

// TestBadgeV0170 — EN/KO README 배지 동기(누락 사고 3회 방지가 범프
// 스크립트 도입 동기).
func TestBadgeV0170(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "release-v0.17.0-blue") {
			t.Errorf("%s 배지 v0.17.0 미기록", p)
		}
	}
}

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
