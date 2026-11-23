package doclint

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
)

func TestRunbookV1140CaseMatrix(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	if want := knowledge.PipelineCaseMatrix(); !strings.Contains(doc, want) {
		t.Errorf("RUNBOOK.md에 파생 케이스 매트릭스 불일치 — want:\n%s", want)
	}
}

func TestRunbookV1140CorrectionHook(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	for _, want := range []string{
		"UserPromptSubmit", "correction_filter.sh", "FLINT_CORRECTION_HOOK",
		"--source correction", "0.4", "재현율 10/10",
		"전체 발언 캡처 금지",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RUNBOOK.md A안 절에 %q 누락", want)
		}
	}
}

func TestRunbookV1140SurvivedRelief(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	for _, want := range []string{
		"survived_decay_relief", "기본 0",
		"survived × relief", "opt-in",
		"knowledge_reads",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RUNBOOK.md survived 완화 서술에 %q 누락", want)
		}
	}
}
