// tools_v1230_test.go — v1.2.3 R17-B5/B6: dispatchWrite kind 서버측 화이트리스트
// 검증, dispatchHot session_id 잔여 제거.
package mcp

import (
	"strings"
	"testing"
)

// TestDispatchWriteRejectsBadKind — B5: 무효 kind는 runFn 도달 전 스키마
// 오류(exit 3, 기존 MCP 오류 포맷). 유효 kind(knowledge)는 통과해
// cli write 조립으로 흐른다. 화이트리스트 원천은 lib/store/validate.go
// kindExpect(knowledge|session|project) — 비수출이라 값 계약으로 동기.
func TestDispatchWriteRejectsBadKind(t *testing.T) {
	calls, _ := installRun(t, `{"status":"committed","id":1}`, 0)

	out, code := DispatchTool("flint_write", map[string]any{
		"kind": "bogus", "payload": map[string]any{"topic": "t"},
	})
	if code != 3 {
		t.Errorf("bogus kind code = %d, want 3", code)
	}
	if !strings.Contains(out, `"error":"schema"`) || !strings.Contains(out, "kind") {
		t.Errorf("bogus kind out = %q, want schema/kind error", out)
	}
	if len(*calls) != 0 {
		t.Errorf("bogus kind reached runFn: %v", (*calls)[0].args)
	}

	for _, kind := range []string{"knowledge", "session", "project"} {
		DispatchTool("flint_write", map[string]any{
			"kind": kind, "payload": map[string]any{},
		})
	}
	if len(*calls) != 3 {
		t.Fatalf("valid kinds calls = %d, want 3", len(*calls))
	}
	for i, kind := range []string{"knowledge", "session", "project"} {
		if got := (*calls)[i].args; !equal(got, []string{"write", kind, "--source", "mcp"}) {
			t.Errorf("kind %q args = %v", kind, got)
		}
	}
}

// TestDispatchHotNoSessionResidue — B6: session_id는 더 이상 --session으로
// 조립되지 않는다(v1.0.1 F7 no-op — 광고 폐기와 실구현 잔여의 불일치 제거).
func TestDispatchHotNoSessionResidue(t *testing.T) {
	calls, _ := installRun(t, "hot summary", 0)
	_, code := DispatchTool("flint_hot", map[string]any{"session_id": "s1"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	wantArgs(t, calls, []string{"hot"})
}
