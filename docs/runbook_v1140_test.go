// runbook_v1140_test.go — v1.14.0 T5: RUNBOOK 3단계 절 계약 — A안 교정
// 훅(구현·킬 스위치·프라이버시 원칙·판정 근거)·survived 감쇠 완화(기본 0
// opt-in)·케이스 매트릭스(T1 PipelineCaseMatrix 파생 — 골든 스냅숏과
// 정합: 문서 표가 lib 등록표 렌더링과 어긋나면 여기서 실패).
package docs

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
)

// TestRunbookV1140CaseMatrix — RUNBOOK의 케이스 매트릭스 표는 lib/knowledge
// 등록표의 파생 렌더링(PipelineCaseMatrix)과 동문자열이어야 한다(단일
// 원천 — 등록표 편집 시 문서 표도 갱신되어야 통과).
func TestRunbookV1140CaseMatrix(t *testing.T) {
	doc := read(t, "RUNBOOK.md")
	if want := knowledge.PipelineCaseMatrix(); !strings.Contains(doc, want) {
		t.Errorf("RUNBOOK.md에 파생 케이스 매트릭스 불일치 — want:\n%s", want)
	}
}

// TestRunbookV1140CorrectionHook — A안 절 계약: UserPromptSubmit 훅 경로
// (correction_filter.sh)·킬 스위치 FLINT_CORRECTION_HOOK(기본 on·off 즉시
// exit 0)·적립 채널(--source correction·0.4·항상 pending)·프라이버시 원칙
// (비매칭 무기록·매칭 조각만 추출·전문 미수신)·go 판정 근거(재현율 10/10).
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

// TestRunbookV1140SurvivedRelief — survived 완화 서술 계약: 산식
// (count − max(0, rate×days − survived×relief)·내부 클램프)·튜닝 키
// survived_decay_relief 기본 0(비활성·opt-in·발행 시점 동작 불변)·관측
// 정의(동일 knowledge_id 세션 2회 이상 재회상 — knowledge_reads 파생).
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
