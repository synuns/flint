// v09_docs_test.go — T3 어댑터 문서 계약 검증. v0.9 하니스 5종 중 codex·
// gemini·copilot 3종(스펙 §2 C1~C3) 문서가 존재하고, 설정 스니펫 키·
// 지시파일·승인 노트를 정확한 포맷으로 담는지 grep 정합 검증한다.
// opencode·cursor는 T4 소유 — 여기서 검증하지 않는다.
package docs

import (
	"strings"
	"testing"
)

// TestV09AdapterDocsCodex — C1: config.toml [mcp_servers.flint] TOML 포맷,
// requires_approval 권장값, 지시파일 AGENTS.md.
func TestV09AdapterDocsCodex(t *testing.T) {
	doc := read(t, "adapters/codex.md")
	for _, want := range []string{
		"[mcp_servers.flint]",
		`command = "flint"`,
		`args = ["mcp"]`,
		`requires_approval = "never"`,
		"AGENTS.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("adapters/codex.md에 %s 없음", want)
		}
	}
}

// TestV09AdapterDocsGemini — C2: settings.json mcpServers + trust:true,
// 지시파일 GEMINI.md.
func TestV09AdapterDocsGemini(t *testing.T) {
	doc := read(t, "adapters/gemini.md")
	for _, want := range []string{
		"mcpServers",
		`"trust": true`,
		"GEMINI.md",
		"flint mcp",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("adapters/gemini.md에 %s 없음", want)
		}
	}
}

// TestV09AdapterDocsCopilot — C3: mcp-config.json, 지시파일
// instructions.md·AGENTS.md 병기(서버 instructions 미반영 버그 E6).
func TestV09AdapterDocsCopilot(t *testing.T) {
	doc := read(t, "adapters/copilot.md")
	for _, want := range []string{
		"mcp-config.json",
		"instructions.md",
		"AGENTS.md",
		"flint mcp",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("adapters/copilot.md에 %s 없음", want)
		}
	}
}
