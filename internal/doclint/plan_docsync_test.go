package doclint

import (
	"strings"
	"testing"
)

func TestPlanEvidenceKindRow(t *testing.T) {
	plan := docStr(t, "PLAN.md")

	if !strings.Contains(plan, "kind 분리 근거") {
		t.Error("PLAN.md §3에 kind 분리 근거 행 없음 (M5)")
	}
	for _, kind := range []string{"에피소드", "의미", "절차"} {
		if !strings.Contains(plan, kind) {
			t.Errorf("PLAN.md에 기억 유형 %q 근거 서술 없음", kind)
		}
	}
	if !strings.Contains(plan, "그래뉼러리티") {
		t.Error("PLAN.md kind 근거에 그래뉼러리티 서술 없음")
	}
	if !strings.Contains(plan, "문서 전용") {
		t.Error("PLAN.md kind 근거에 동작 변경 없음(문서 전용) 표기 없음")
	}

	if !strings.Contains(plan, "교정 입구") {
		t.Error("PLAN.md에 교정 입구 결정 행 없음 (M6)")
	}
	if !strings.Contains(plan, "B안") || !strings.Contains(plan, "A안") {
		t.Error("PLAN.md 교정 입구 결정에 B안 기본·A안 한정 병행 표기 없음")
	}
	if !strings.Contains(plan, "2026-11-25-ax-matrix.md") {
		t.Error("PLAN.md 교정 입구 결정에 영역 D 감사 권고 원천 참조 없음")
	}
	if !strings.Contains(plan, "v1.34.0") {
		t.Error("PLAN.md 교정 입구 결정에 v1.34.0 전달 계열 연결 명시 없음")
	}
	if !strings.Contains(plan, "신규 훅 신설 금지") {
		t.Error("PLAN.md 교정 입구 결정에 A안 한정 조건(신규 훅 신설 금지·기존 훅 확장만) 없음")
	}
}
