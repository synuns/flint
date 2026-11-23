package doclint

import (
	"strings"
	"testing"
)

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

func TestRunbookV1170EchoCaseRow(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	for _, want := range []string{"에코 검토", "v1.17.0"} {
		if !strings.Contains(doc, want) {
			t.Errorf("RUNBOOK.md에 %q 누락", want)
		}
	}
}
