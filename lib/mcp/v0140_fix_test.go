// v0140_fix_test.go — v0.14.0 리뷰 수정 회귀(P1): MCP 도구 응답의 isError는
// 빈 출력의 exit 1(제로히트 침묵 계약)을 오류로 취급하지 않는다.
package mcp

import (
	"testing"
)

// TestToolsCallIsErrorRequiresOutput — exit 1은 출력 유무로 구분한다:
// 제로히트 read(exit 1·출력 "")는 isError:false·text ""(침묵 정상 결과),
// 오류(exit 1·에러 JSON 한 줄)는 isError:true. 모든 flint 오류는 errLine을
// 출력하므로 빈 출력+exit 1은 침묵 계약뿐이다.
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
			// handleRPC 직접 호출이라 content는 JSON 직렬화 전 값
			// ([]map[string]any — Serve 경로는 한 줄로 직렬화된다).
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
