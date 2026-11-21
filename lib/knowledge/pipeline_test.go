// pipeline_test.go — v1.14.0 T1: the knowledge-pipeline case registry
// contract tests. The registry enumerates every intake case (who/what
// feeds the pipeline), and the derived RUNBOOK matrix plus the
// sourceRegistry sync are guarded here — a drifting registry, source, or
// doc table fails these tests.
package knowledge

import (
	"strconv"
	"strings"
	"testing"
)

// wantPipelineCaseIDs — the plan's eight cases in enumeration order
// (docs/plans/2026-11-21-flint-v1140.md T1 seven + the v1.17.0 echo case,
// gate-adjusted: survived·echo carry no source).
var wantPipelineCaseIDs = []string{
	"correction-b", // correction 0.7 (v1.11.0)
	"correction-a", // correction 0.4 (v1.14.0)
	"gap",          // recall-failure/repeat (v1.12.0)
	"mcp",          // MCP tool observation (v0.2.6 lineage)
	"session-distill",
	"manual",
	"survived", // observation-only, no source (v1.14.0)
	"echo",     // observation/judgment support, no source (v1.17.0)
}

// TestPipelineCaseRegistryComplete — the registry holds exactly the eight
// plan cases (unique IDs, plan order) and every descriptive field is
// populated: ID·Name·Signal·Confidence·Since are always non-empty; Source
// is empty only for the observation-only cases. The fixed confidences stay
// in sync with the write.go payload constants (gap derives from
// gapCandidateConfidence — editing the constant without the registry, or
// vice versa, fails here).
func TestPipelineCaseRegistryComplete(t *testing.T) {
	if len(pipelineCases) != len(wantPipelineCaseIDs) {
		t.Fatalf("등록표 크기=%d, want %d(8케이스)", len(pipelineCases), len(wantPipelineCaseIDs))
	}
	seen := make(map[string]bool, len(pipelineCases))
	for i, c := range pipelineCases {
		if c.ID != wantPipelineCaseIDs[i] {
			t.Errorf("등록표[%d] ID=%q, want %q(플랜 열거 순서)", i, c.ID, wantPipelineCaseIDs[i])
		}
		if seen[c.ID] {
			t.Errorf("ID %q 중복", c.ID)
		}
		seen[c.ID] = true
		for _, empty := range []struct{ field, val string }{
			{"Name", c.Name}, {"Signal", c.Signal},
			{"Confidence", c.Confidence}, {"Since", c.Since},
		} {
			if strings.TrimSpace(empty.val) == "" {
				t.Errorf("케이스 %q의 %s 비어 있음", c.ID, empty.field)
			}
		}
	}
	byID := make(map[string]PipelineCase, len(pipelineCases))
	for _, c := range pipelineCases {
		byID[c.ID] = c
	}
	// The gap case's fixed confidence must equal the write.go payload
	// constant (single source — no duplicate literals drifting).
	if got, want := byID["gap"].Confidence,
		strconv.FormatFloat(gapCandidateConfidence, 'f', -1, 64); got != want {
		t.Errorf("gap confidence=%q, want %q(gapCandidateConfidence와 동치)", got, want)
	}
	// Plan-fixed values: correction lineages and the observation-only cases.
	fixed := map[string][3]string{ // Confidence, Tag, Since
		"correction-b": {"0.7", "correction", "v1.11.0"},
		"correction-a": {"0.4", "correction", "v1.14.0"},
		"gap":          {"0.4", "gap-candidate", "v1.12.0"},
		"survived":     {"관측", "", "v1.14.0"},
		"echo":         {"관측", "", "v1.17.0"},
	}
	for id, want := range fixed {
		c, ok := byID[id]
		if !ok {
			t.Fatalf("케이스 %q 부재", id)
		}
		if c.Confidence != want[0] || c.Tag != want[1] || c.Since != want[2] {
			t.Errorf("%s = (conf %q·tag %q·since %q), want (%q·%q·%q)",
				id, c.Confidence, c.Tag, c.Since, want[0], want[1], want[2])
		}
	}
	if byID["survived"].Source != "" {
		t.Errorf("survived source=%q, want \"\"(관측 전용 — source 없음)", byID["survived"].Source)
	}
}

// TestPipelineCaseSourceRegistrySync — every source-bearing case maps to
// a registered metering source (write.go sourceRegistry), and each of the
// five registry sources is covered by at least one case (the registry is
// the complete intake map, not a subset). Exactly two cases are
// observation-only (survived·echo — no separate metering source).
func TestPipelineCaseSourceRegistrySync(t *testing.T) {
	covered := make(map[string]bool, len(sourceRegistry))
	emptySource := 0
	for _, c := range pipelineCases {
		if c.Source == "" {
			emptySource++
			continue
		}
		if !validSources[c.Source] {
			t.Errorf("케이스 %q의 source %q가 sourceRegistry에 없음", c.ID, c.Source)
		}
		covered[c.Source] = true
	}
	if emptySource != 2 {
		t.Errorf("source 없는 케이스 수=%d, want 2(survived·echo 관측 전용)", emptySource)
	}
	for _, s := range sourceRegistry {
		if !covered[s] {
			t.Errorf("source %q를 담은 케이스 없음(등록표↔sourceRegistry 불완전 커버)", s)
		}
	}
}

// wantPipelineCaseMatrix — the golden RUNBOOK-shaped snapshot of the
// derived case matrix (T5 embeds this table; the derivation and the
// snapshot change together or not at all).
const wantPipelineCaseMatrix = `| 케이스 | 입구 신호 | source | 태그 | confidence | 구현 |
| --- | --- | --- | --- | --- | --- |
| 교정 B안(에이전트 계약) | 사용자 교정을 에이전트가 인지해 즉시 적립(스킬 지침) | correction | correction | 0.7 | v1.11.0 |
| 교정 A안(훅 감지) | UserPromptSubmit 훅 — 로컬 필터가 교정 마커 매칭 | correction | correction | 0.4 | v1.14.0 |
| 회상 실패·반복 | owned_miss 당일 누적이 gap_query_threshold 도달 | gap | gap-candidate | 0.4 | v1.12.0 |
| MCP 도구 호출 | flint MCP 서버 경로 관측 | mcp | — | 관측 | v0.2.6 |
| 세션 증류 | PostCompact 훅 — 압축 요약의 세션 행 적립 | hook | — | 0.5 | v0.27 |
| 수동 | 사람 명시 적립(confidence·태그 지정) | manual | — | 지정 | v0.1 |
| 검증된 성공 | 동일 (knowledge_id, session) 재회상 관측 | — | — | 관측 | v1.14.0 |
| 에코 검토 | write 응답 echo 필드 — 근접 활성 행 duplicates/contradicts 보고 | — | — | 관측 | v1.17.0 |
`

// TestPipelineCaseMatrixSnapshot — the derived RUNBOOK matrix equals the
// golden snapshot (a registry edit without a snapshot update fails), and
// the derivation guard holds: every registry case renders its own row.
func TestPipelineCaseMatrixSnapshot(t *testing.T) {
	got := PipelineCaseMatrix()
	if got != wantPipelineCaseMatrix {
		t.Fatalf("파생 매트릭스 스냅숏 불일치:\ngot:\n%s\nwant:\n%s", got, wantPipelineCaseMatrix)
	}
	for _, c := range pipelineCases {
		if !strings.Contains(got, "| "+c.Name+" |") {
			t.Errorf("매트릭스에 케이스 %q(%s) 행 없음 — 파생 누락", c.Name, c.ID)
		}
	}
}
