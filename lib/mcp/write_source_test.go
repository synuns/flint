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

// TestDispatchPendingJSONMode — v0.32.0: flint_pending은 pending --json 조립.
func TestDispatchPendingJSONMode(t *testing.T) {
	calls, _ := installRun(t, `{"status":"ok","items":[]}`, 0)
	DispatchTool("flint_pending", map[string]any{})
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	args := (*calls)[0].args
	if len(args) < 2 || args[0] != "pending" || args[1] != "--json" {
		t.Errorf("flint_pending 조립 = %v, want [pending --json]", args)
	}
}

// TestDispatchApproveAssembly — v0.32.0: id/supersede 검증·조립.
func TestDispatchApproveAssembly(t *testing.T) {
	calls, _ := installRun(t, `{"status":"approved","id":7}`, 0)
	if _, code := DispatchTool("flint_approve", map[string]any{"id": 7}); code != 0 {
		t.Fatalf("정상 승격 exit=%d", code)
	}
	got := (*calls)[0].args
	want := []string{"approve", "--id", "7"}
	if !sliceEq(got, want) {
		t.Errorf("조립 = %v, want %v", got, want)
	}
	// supersede 동반.
	calls, _ = installRun(t, `{"status":"approved","id":7,"superseded":3}`, 0)
	DispatchTool("flint_approve", map[string]any{"id": 7, "supersede": 3})
	got = (*calls)[0].args
	want = []string{"approve", "--id", "7", "--supersede", "3"}
	if !sliceEq(got, want) {
		t.Errorf("supersede 조립 = %v, want %v", got, want)
	}
	// 검증 실패 3종 — 실행 안 함.
	for _, bad := range []map[string]any{
		{"id": 0}, {"id": -1}, {"id": 7, "supersede": 7},
	} {
		calls, _ = installRun(t, "", 0)
		if _, code := DispatchTool("flint_approve", bad); code != 3 {
			t.Errorf("무효 %v exit=%d, want 3", bad, code)
		}
		if len(*calls) != 0 {
			t.Errorf("무효 입력인데 실행됨: %v", bad)
		}
	}
}

// sliceEq — 문자열 슬라이스 동등.
func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
