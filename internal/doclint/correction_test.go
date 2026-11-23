package doclint

import (
	"strings"
	"testing"
)

const correctionSources = "hook|manual|mcp|correction"

func TestSkillCorrectionGuidance(t *testing.T) {
	s := docStr(t, "../flint-plugin/skills/flint/SKILL.md")

	for _, want := range []string{
		"write knowledge --source correction",
		"0.7",
		"추측 금지",
		"전량 캡처 금지",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md 교정 지침에 %q 누락", want)
		}
	}
}

func TestSkillSourceListMatchesRegistry(t *testing.T) {
	s := docStr(t, "../flint-plugin/skills/flint/SKILL.md")

	if !strings.Contains(s, correctionSources) {
		t.Errorf("SKILL.md에 등록표 소스 목록 %s 없음", correctionSources)
	}
}

func TestRunbookPipelineCorrection(t *testing.T) {
	s := docStr(t, "RUNBOOK.md")

	for _, want := range []string{
		"지식화 파이프라인",
		"write knowledge --source correction",
		"B안",
		"A안",
		"correction_confidence",
		"correction_daily_max",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("RUNBOOK.md 파이프라인 1단계 절에 %q 누락", want)
		}
	}
}
