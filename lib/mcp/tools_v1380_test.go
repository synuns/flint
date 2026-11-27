package mcp

import (
	"strings"
	"testing"
)

func TestMcpFlintWorkManifestAndDispatch(t *testing.T) {
	var names []string
	for _, td := range ToolsManifest() {
		names = append(names, td.Name)
	}
	if len(names) != 6 {
		t.Fatalf("len(ToolsManifest)=%d (%v), want 6 with flint_work", len(names), names)
	}
	found := false
	for _, n := range names {
		if n == "flint_work" {
			found = true
		}
	}
	if !found {
		t.Fatalf("flint_work manifest 미포함: %v", names)
	}

	calls, _ := installRun(t, `{"id":1,"status":"open"}`, 0)

	if _, code := DispatchTool("flint_work", map[string]any{"action": "bogus"}); code != 3 {
		t.Errorf("미지 action exit=%d, want 3", code)
	}
	if _, code := DispatchTool("flint_work", map[string]any{"action": "open"}); code != 3 {
		t.Errorf("task 부재 open exit=%d, want 3", code)
	}
	if _, code := DispatchTool("flint_work", map[string]any{"action": "open", "task": "   "}); code != 3 {
		t.Errorf("공백 task open exit=%d, want 3", code)
	}
	if _, code := DispatchTool("flint_work", map[string]any{"action": "resolve"}); code != 3 {
		t.Errorf("id 부재 resolve exit=%d, want 3", code)
	}

	if out, code := DispatchTool("flint_work", map[string]any{
		"action": "open", "task": "fix path", "detail": "d", "next": "rerun ci", "project": float64(3),
	}); code != 0 || !strings.Contains(out, `"status":"open"`) {
		t.Errorf("open dispatch=(%q,%d), want exit 0+status open", out, code)
	}
	wantCallArgs(t, calls, 0, []string{"state", "open", "--task", "fix path", "--detail", "d", "--next", "rerun ci", "--project", "3"})

	if out, code := DispatchTool("flint_work", map[string]any{"action": "update", "task": "fix path"}); code != 0 {
		t.Errorf("update dispatch=(%q,%d), want exit 0", out, code)
	}
	wantCallArgs(t, calls, 1, []string{"state", "open", "--task", "fix path"})

	if out, code := DispatchTool("flint_work", map[string]any{"action": "resolve", "id": float64(7), "by": float64(2)}); code != 0 {
		t.Errorf("resolve dispatch=(%q,%d), want exit 0", out, code)
	}
	wantCallArgs(t, calls, 2, []string{"state", "resolve", "--id", "7", "--by", "2"})

	if out, code := DispatchTool("flint_work", map[string]any{"action": "list", "all": true}); code != 0 {
		t.Errorf("list dispatch=(%q,%d), want exit 0", out, code)
	}
	wantCallArgs(t, calls, 3, []string{"state", "list", "--all"})
}

func wantCallArgs(t *testing.T, calls *[]call, i int, want []string) {
	t.Helper()
	if len(*calls) <= i {
		t.Fatalf("runFn 호출 %d회 부족, want %d+", len(*calls), i+1)
	}
	if got := (*calls)[i].args; !equal(got, want) {
		t.Errorf("call[%d].args = %v, want %v", i, got, want)
	}
}

func TestMcpApproveClaimGateMapping(t *testing.T) {
	gateMsg := `{"error":"usage","message":"claim gate: knowledge row claim is unset (backfill=event) — use --claim decision|procedure|lesson|event"}`
	calls, _ := installRun(t, gateMsg, 3)
	if out, code := DispatchTool("flint_approve", map[string]any{"id": float64(54)}); code != 3 || !strings.Contains(out, "claim gate") {
		t.Errorf("무 claim 승격=(%q,%d), want exit 3+claim gate", out, code)
	}
	wantCallArgs(t, calls, 0, []string{"approve", "--id", "54"})

	calls2, _ := installRun(t, `{"status":"approved","id":54}`, 0)
	if out, code := DispatchTool("flint_approve", map[string]any{"id": float64(54), "claim": "decision"}); code != 0 || !strings.Contains(out, `"status":"approved"`) {
		t.Errorf("--claim 승격=(%q,%d), want exit 0+approved", out, code)
	}
	wantCallArgs(t, calls2, 0, []string{"approve", "--id", "54", "--claim", "decision"})
}
