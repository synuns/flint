// write_source_test.go — T61 회귀: MCP write 도구는 cli.Run 조립에
// --source mcp를 전달해 유입이 source=mcp로 계측되어야 한다(미전달 시 manual).
package mcp

import "testing"

// TestDispatchWriteSourceMCP — flint_write 조립 인자에 --source mcp 존재.
func TestDispatchWriteSourceMCP(t *testing.T) {
	calls, _ := installRun(t, `{"status":"committed","id":1}`, 0)
	DispatchTool("flint_write", map[string]any{
		"kind":    "knowledge",
		"payload": map[string]any{"topic": "t", "summary": "s"},
	})
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	for i, a := range (*calls)[0].args {
		if a == "--source" {
			if i+1 >= len((*calls)[0].args) || (*calls)[0].args[i+1] != "mcp" {
				t.Errorf("--source value = %v, want mcp", (*calls)[0].args[i+1:])
			}
			return
		}
	}
	t.Errorf("args = %v, want --source mcp (T61: MCP 유입 manual 계측 방지)", (*calls)[0].args)
}
