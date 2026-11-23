package knowledge

import (
	"fmt"
	"strings"
)

type PipelineCase struct {
	ID         string
	Name       string
	Signal     string
	Source     string
	Tag        string
	Confidence string
	Since      string
}

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
	{
		ID:         "echo",
		Name:       "에코 검토",
		Signal:     "write 응답 echo 필드 — 근접 활성 행 duplicates/contradicts 보고",
		Source:     "",
		Tag:        "",
		Confidence: "관측",
		Since:      "v1.17.0",
	},
}

func PipelineCases() []PipelineCase {
	out := make([]PipelineCase, len(pipelineCases))
	copy(out, pipelineCases)
	return out
}

const matrixDash = "—"

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
