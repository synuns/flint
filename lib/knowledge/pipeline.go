// pipeline.go — v1.14.0 T1: the knowledge-pipeline case registry. The
// seven intake cases (who/what feeds long-term memory, with which
// metering source, tag, confidence, and implementing version) live here
// as the single enumeration; the RUNBOOK case matrix is derived from it
// (no hand-maintained doc table drifting from the code). The
// source-bearing cases must stay in sync with sourceRegistry
// (write.go) — the contract test guards both directions.
package knowledge

import (
	"fmt"
	"strings"
)

// PipelineCase — one intake case of the knowledge pipeline. Source is
// the metering source of the write; "" marks an observation-only case
// (no write, so no source). Tag is the automatic row-level tag ("" =
// none or caller-specified). Confidence is the payload's confidence in
// RUNBOOK display form: a fixed decimal literal ("0.7"), "관측"
// (observed, no fixed value), or "지정" (caller-specified). Since is the
// version that implemented the case.
type PipelineCase struct {
	ID         string // stable short identifier (doc anchor, test order)
	Name       string // Korean display name (RUNBOOK matrix)
	Signal     string // entry signal — what triggers the intake
	Source     string // metering source; "" = observation-only
	Tag        string // automatic row tag; "" = none/caller-specified
	Confidence string // fixed decimal, "관측", or "지정"
	Since      string // implementing version
}

// pipelineCases — the seven knowledge-pipeline intake cases in plan
// enumeration order (docs/plans/2026-11-21-flint-v1140.md T1):
//
//  1. correction-b — the v1.11.0 agent-contract correction (B안): an
//     agent noticing a user correction writes it on the correction
//     channel (skill-guided; default 0.7 via correction_confidence).
//  2. correction-a — the v1.14.0 hook-detected correction (A안): the
//     UserPromptSubmit local filter writes only matched correction
//     fragments at the attenuated 0.4 (machine signal, pending gate).
//  3. gap — the v1.12.0 recall-failure/repeat candidate: an owned_miss
//     count reaching gap_query_threshold accrues one pending candidate
//     (fixed 0.4 = gapCandidateConfidence, tag gap-candidate).
//  4. mcp — the MCP tool-call observation path (v0.2.6 lineage): intake
//     and read metering through the flint MCP server, no fixed
//     confidence.
//  5. session-distill — the v0.27 session distillation: the PostCompact
//     hook accrues the compaction summary as a session row (fixed 0.5 —
//     pending gate, promoted via approve).
//  6. manual — the founding human channel: explicit write with
//     caller-specified confidence and tags.
//  7. survived — the v1.14.0 verified-success observation: a re-recall
//     of the same (knowledge_id, session) is an observation signal, not
//     a write — no metering source (decay relief consumes it, T3/T4).
var pipelineCases = []PipelineCase{
	{
		ID:         "correction-b",
		Name:       "교정 B안(에이전트 계약)",
		Signal:     "사용자 교정을 에이전트가 인지해 즉시 적립(스킬 지침)",
		Source:     "correction",
		Tag:        "correction",
		Confidence: "0.7",
		Since:      "v1.11.0",
	},
	{
		ID:         "correction-a",
		Name:       "교정 A안(훅 감지)",
		Signal:     "UserPromptSubmit 훅 — 로컬 필터가 교정 마커 매칭",
		Source:     "correction",
		Tag:        "correction",
		Confidence: "0.4",
		Since:      "v1.14.0",
	},
	{
		ID:         "gap",
		Name:       "회상 실패·반복",
		Signal:     "owned_miss 당일 누적이 gap_query_threshold 도달",
		Source:     "gap",
		Tag:        "gap-candidate",
		Confidence: "0.4",
		Since:      "v1.12.0",
	},
	{
		ID:         "mcp",
		Name:       "MCP 도구 호출",
		Signal:     "flint MCP 서버 경로 관측",
		Source:     "mcp",
		Tag:        "",
		Confidence: "관측",
		Since:      "v0.2.6",
	},
	{
		ID:         "session-distill",
		Name:       "세션 증류",
		Signal:     "PostCompact 훅 — 압축 요약의 세션 행 적립",
		Source:     "hook",
		Tag:        "",
		Confidence: "0.5",
		Since:      "v0.27",
	},
	{
		ID:         "manual",
		Name:       "수동",
		Signal:     "사람 명시 적립(confidence·태그 지정)",
		Source:     "manual",
		Tag:        "",
		Confidence: "지정",
		Since:      "v0.1",
	},
	{
		ID:         "survived",
		Name:       "검증된 성공",
		Signal:     "동일 (knowledge_id, session) 재회상 관측",
		Source:     "",
		Tag:        "",
		Confidence: "관측",
		Since:      "v1.14.0",
	},
}

// PipelineCases — a copy of the registry (callers cannot mutate the
// package-level enumeration).
func PipelineCases() []PipelineCase {
	out := make([]PipelineCase, len(pipelineCases))
	copy(out, pipelineCases)
	return out
}

// matrixDash — the RUNBOOK empty-cell marker for source/tag fields that
// do not apply to a case.
const matrixDash = "—"

// PipelineCaseMatrix — the RUNBOOK case-matrix table (markdown) derived
// from pipelineCases. T5 embeds this string in docs; the snapshot test
// pins the rendering, so registry and doc change together.
func PipelineCaseMatrix() string {
	var b strings.Builder
	b.WriteString("| 케이스 | 입구 신호 | source | 태그 | confidence | 구현 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, c := range pipelineCases {
		source, tag := c.Source, c.Tag
		if source == "" {
			source = matrixDash
		}
		if tag == "" {
			tag = matrixDash
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			c.Name, c.Signal, source, tag, c.Confidence, c.Since)
	}
	return b.String()
}
