// runbook_v094_test.go — RUNBOOK.md에 v0.9.3 실증 복구 절차(AS OF 선택
// 복원)와 사고 실례 커밋이 문서화되었는지 검증한다(v0.9.4 R2 계약).
package docs

import (
	"os"
	"strings"
	"testing"
)

func runbookStr(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("RUNBOOK.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunbookHasASOfProcedure(t *testing.T) {
	s := runbookStr(t)
	for _, kw := range []string{"INSERT INTO knowledge SELECT", "AS OF", "dolt_revert", "선택 복원"} {
		if !strings.Contains(s, kw) {
			t.Errorf("절차 키워드 누락: %q", kw)
		}
	}
}

func TestRunbookHasIncidentReference(t *testing.T) {
	s := runbookStr(t)
	if !strings.Contains(s, "jkfa904") || !strings.Contains(s, "001nnpn") {
		t.Error("v093 사고 실례 커밋 미언급")
	}
}
