// v0180_docs_test.go — v0.18.0 회상 우선 메모리 문서 회귀. 버전 동기는
// TestVersionArtifactsSync(v0171 범용화)가 담당 — 여기는 마일스톤 기재만.
package docs

import (
	"strings"
	"testing"
)

// TestVersion0180Milestone — PLAN.md에 v0.18.0 엔트리와 회상 경고 임계 키.
func TestVersion0180Milestone(t *testing.T) {
	doc := docStr(t, "PLAN.md")
	for _, want := range []string{"v0.18.0", "회상 우선 메모리"} {
		if !strings.Contains(doc, want) {
			t.Errorf("PLAN.md에 %q 누락", want)
		}
	}
	// recall_warn_writes는 thresholds 레지스트리+TUNING §2 동기 키.
	tun := docStr(t, "TUNING.md")
	if !strings.Contains(tun, "recall_warn_writes") {
		t.Error("TUNING.md에 recall_warn_writes 누락(thresholds 동기)")
	}
}
