// runbook_v1170_test.go — v1.17.0 T5: RUNBOOK 에코 절 계약 — 개념·echo
// 필드 해석(duplicates|contradicts·score)·판정 가이드(제안만·에이전트
// 판정)·관측 키 3종(correction_approved·gap_approved·survived_sessions)·
// maintain --decay-audit의 survived 스냅숏.
package doclint

import (
	"strings"
	"testing"
)

// TestRunbookV1170EchoSection — 에코 절이 필수 서술을 전부 담는다.
func TestRunbookV1170EchoSection(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	for _, want := range []string{
		"echo",
		"duplicates",
		"contradicts",
		"score",
		"echo_top_k",
		"판정은 에이전트",
		"correction_approved",
		"gap_approved",
		"survived_sessions",
		"--decay-audit",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RUNBOOK.md 에코 절에 %q 누락", want)
		}
	}
}

// TestRunbookV1170EchoCaseRow — 케이스 등록표의 에코 행(v1.17.0·관측)이
// 파생 매트릭스와 함께 문서에 존재한다(정합 자체는
// TestRunbookV1140CaseMatrix 파생 검증이 담당 — 여기선 행 존재만).
func TestRunbookV1170EchoCaseRow(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	for _, want := range []string{"에코 검토", "v1.17.0"} {
		if !strings.Contains(doc, want) {
			t.Errorf("RUNBOOK.md에 %q 누락", want)
		}
	}
}
