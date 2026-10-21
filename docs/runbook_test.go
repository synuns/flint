// runbook_test.go — RUNBOOK.md에 운영 4절(감사·오염 대응·복구 훈련·백업
// 보존)이 모두 존재하는지 검증한다(T53 — 신뢰성 런북의 계약).
package docs

import (
	"strings"
	"testing"
)

// TestRunbookSections — RUNBOOK.md에 audit·dolt_revert·drill·backup_keep
// 4키워드가 각 절에 포함되어 있는지 strings.Contains로 검증.
func TestRunbookSections(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	for _, kw := range []string{"audit", "dolt_revert", "drill", "backup_keep"} {
		if !strings.Contains(doc, kw) {
			t.Errorf("RUNBOOK.md에 %s 절 없음", kw)
		}
	}
}
