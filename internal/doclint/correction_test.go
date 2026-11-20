// correction_v1110_test.go — v1.11.0 T4 스킬·RUNBOOK 교정 채널 문서 계약.
// 플랜 docs/plans/archive/2026-11/2026-11-20-flint-v1110.md T4: plugin 스킬의 교정 지침과
// RUNBOOK 파이프라인 1단계 절이 코드 등록표·채널 계약과 정합인지 검증.
package doclint

import (
	"strings"
	"testing"
)

// correctionSources — lib/knowledge/write.go sourceRegistry의 표기 순
// 그대로(드리프트 시 lib 측 동질 가드 TestSourceRegistryDerivationGuard가
// 먼저 실패 — 이 표기는 문서가 등록표와 일치함을 기계 확인한다).
const correctionSources = "hook|manual|mcp|correction"

// TestSkillCorrectionGuidance — SKILL.md 교정 지침 계약: 즉시 적립
// 커맨드·교정 내용만 최소 추출·추측 금지·전량 캡처 금지·payload 미지정
// 시 기본 confidence 0.7(protocol·numeric 관점).
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

// TestSkillSourceListMatchesRegistry — SKILL.md의 --source 목록 표기가
// 등록표(hook|manual|mcp|correction)와 일치한다(file 관점 — 등록표 파생).
func TestSkillSourceListMatchesRegistry(t *testing.T) {
	s := docStr(t, "../flint-plugin/skills/flint/SKILL.md")

	if !strings.Contains(s, correctionSources) {
		t.Errorf("SKILL.md에 등록표 소스 목록 %s 없음", correctionSources)
	}
}

// TestRunbookPipelineCorrection — RUNBOOK 파이프라인 1단계 절: 교정 채널
// 계약·B안(에이전트 계약 유도) 룰링·임계값 2키·후속(A안·나머지 케이스)
// 참조·기존 pending 회로 승계 서술.
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
