// protocol_test.go — v0.9 통합 시퀀스(Review Focus: 현대 클라이언트 협상·
// 알림 무응답·ping·tools/list). T1의 협상 코드를 RunMCPServer 전체 경로로
// 검증한다(알림 제외 응답 3줄).
package mcp

import (
	"bytes"
	"strings"
	"testing"
)

// TestProtocolSequenceModernClient — 2025-06-18 클라이언트 시퀀스:
// initialize(echo·instructions) → notifications/initialized(무응답) →
// ping(빈 result) → tools/list(3종). 알림을 제외한 응답은 정확히 3줄.
func TestProtocolSequenceModernClient(t *testing.T) {
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}` + "\n")
	if code := RunMCPServer(&in, &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (EOF 정상 종료)", code)
	}
	lines := decodeLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("responses = %d, want 3(알림 무응답): %q", len(lines), out.String())
	}
	init := lines[0]["result"].(map[string]any)
	if got := init["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want echo 2025-06-18", got)
	}
	ins, _ := init["instructions"].(string)
	if !strings.Contains(ins, "flint_read") || !strings.Contains(ins, "flint_write") {
		t.Errorf("instructions 누락: %q", ins)
	}
	ping := lines[1]["result"].(map[string]any)
	if len(ping) != 0 {
		t.Errorf("ping result = %v, want 빈 객체", ping)
	}
	tools, _ := lines[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Errorf("tools/list = %v건, want 3", len(tools))
	}
	for i, id := range []float64{1, 2, 3} {
		if got, ok := lines[i]["id"].(float64); !ok || got != id {
			t.Errorf("lines[%d].id = %v, want %v", i, lines[i]["id"], id)
		}
	}
}
