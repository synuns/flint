// v0140_fix_test.go — the MCP tool response's isError does not treat an
// exit 1 with empty output (the zero-hit silence contract) as an error.
package mcp

import (
	"testing"
)

// TestToolsCallIsErrorRequiresOutput — exit 1 is distinguished by the
// presence of output: a zero-hit read (exit 1, output "") is
// isError:false with text "" (the normal silence outcome); an error
// (exit 1, one JSON error line) is isError:true. Every flint error prints
// an errLine, so empty output + exit 1 can only be the silence contract.
func TestToolsCallIsErrorRequiresOutput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		wantErr bool
	}{
		{"zero-hit silence", "", false},
		{"error line", `{"error":"storage","message":"연결 실패"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, real := installRun(t, tc.out, 1)
			if real {
				t.Skip("real runFn: 실DB 경로는 tools_test 담당")
			}
			res, err := handleRPC("tools/call", map[string]any{
				"name":      "flint_read",
				"arguments": map[string]any{"query": "q"},
			})
			if err != nil {
				t.Fatal(err)
			}
			m := res.(map[string]any)
			if m["isError"] != tc.wantErr {
				t.Errorf("isError = %v, want %v (out=%q code=1)", m["isError"], tc.wantErr, tc.out)
			}
			// handleRPC is called directly, so content is the
			// pre-serialization value ([]map[string]any — the Serve path
			// serializes it into one line).
			content, _ := m["content"].([]map[string]any)
			if len(content) != 1 {
				t.Fatalf("content = %v, want 1건", m["content"])
			}
			if text := content[0]["text"]; text != tc.out {
				t.Errorf("text = %v, want %q(도구 출력 원문)", text, tc.out)
			}
		})
	}
}
