package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0160(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0160(t *testing.T) {
	if !strings.Contains(docStrV0160(t, "PLAN.md"), "v0.16.0") {
		t.Error("PLAN.md v0.16.0 마일스톤 누락")
	}
}

func TestBadgeIsV0160(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0160(t, p), "release-v0.16.0") {
			t.Errorf("%s 배지 v0.16.0 미갱신", p)
		}
	}
}

// TestCiTriggersOnVersionTags — branches 필터만 있는 push 트리거는 태그
// push를 발동시키지 않는다(GitHub Actions 필터 규칙). release 잡이
// 돌려면 tags: ["v*"] 트리거가 명시돼 있어야 한다.
func TestCiTriggersOnVersionTags(t *testing.T) {
	yml := docStrV0160(t, "../.github/workflows/ci.yml")
	if !strings.Contains(yml, `tags: ["v*"]`) {
		t.Error("ci.yml push 트리거에 tags: [\"v*\"] 없음 — 태그 push로 release 잡 미발동")
	}
}

func TestReadmePluginInstall(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0160(t, p), "marketplace add synuns/flint") {
			t.Errorf("%s 플러그인 설치 안내 누락", p)
		}
	}
}
