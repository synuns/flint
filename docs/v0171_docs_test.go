// v0171_docs_test.go — 버전 동기 가드의 범용화(v0.17.1): 단일 버전
// 소스(VERSION, v0.16.7)를 기준으로 plugin.json·배지가 일치하는지 검증.
// 버전별 하드코딩(TestVersion0170 등)을 폐지 — 범프마다 테스트를 고칠
// 필요가 없어지고 누락 가드는 그대로 유지된다.
package docs

import (
	"regexp"
	"strings"
	"testing"
)

// TestVersionArtifactsSync — VERSION = plugin.json = EN/KO 배지 4종 일치.
func TestVersionArtifactsSync(t *testing.T) {
	ver := strings.TrimSpace(docStr(t, "../VERSION"))
	if m := regexp.MustCompile(`^\d+\.\d+\.\d+$`).FindString(ver); m == "" {
		t.Fatalf("VERSION 형식 오류: %q", ver)
	}
	got := regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`).
		FindStringSubmatch(docStr(t, "../flint-plugin/.claude-plugin/plugin.json"))
	if got == nil || got[1] != ver {
		t.Errorf("plugin.json version=%v, want %s(VERSION)", got, ver)
	}
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "release-v"+ver+"-blue") {
			t.Errorf("%s 배지 v%s 미기록(VERSION 불일치)", p, ver)
		}
	}
}
