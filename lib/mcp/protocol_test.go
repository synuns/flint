package mcp

import (
	"bytes"
	"strings"
	"testing"
)

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
	if len(tools) != 5 {
		t.Errorf("tools/list = %v건, want 5", len(tools))
	}
	for i, id := range []float64{1, 2, 3} {
		if got, ok := lines[i]["id"].(float64); !ok || got != id {
			t.Errorf("lines[%d].id = %v, want %v", i, lines[i]["id"], id)
		}
	}
}
