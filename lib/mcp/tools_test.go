// tools_test.go — 도구 3종 왕복 테스트. runFn seam: 기본 fake,
// SUPERLITE_REAL=1이면 실 cli.Run 위임(인자 기록은 유지, 실DB 미기동 시 스킵).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"flint/lib/db"
)

// call — runFn 호출 기록(조립된 CLI 인자 + stdin).
type call struct {
	args  []string
	stdin string
}

// installRun — runFn 교체. 기본 fake(고정 출력·코드 반환), SUPERLITE_REAL=1이면
// 실 cli.Run을 감싸 인자만 기록. 반환값 real은 출력 단정 가능 여부.
func installRun(t *testing.T, out string, code int) (*[]call, bool) {
	t.Helper()
	var calls []call
	real := os.Getenv("SUPERLITE_REAL") == "1"
	if real {
		pool, err := db.Connect(context.Background(), os.Getenv("FLINT_DSN"))
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		pool.Close()
	}
	prev := runFn
	runFn = func(args []string, stdin io.Reader, stdout io.Writer) int {
		b, _ := io.ReadAll(stdin)
		calls = append(calls, call{args: args, stdin: string(b)})
		if real {
			return prev(args, bytes.NewReader(b), stdout)
		}
		fmt.Fprint(stdout, out)
		return code
	}
	t.Cleanup(func() { runFn = prev })
	return &calls, real
}

// wantArgs — 기록된 첫 호출의 CLI 인자 단정.
func wantArgs(t *testing.T, calls *[]call, want []string) {
	t.Helper()
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	if got := (*calls)[0].args; !equal(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
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

// TestToolsManifest — 도구 3종 정의: 이름·설명·스키마 존재.
func TestToolsManifest(t *testing.T) {
	tools := ToolsManifest()
	if len(tools) != 3 {
		t.Fatalf("len(ToolsManifest) = %d, want 3", len(tools))
	}
	want := []string{"flint_read", "flint_write", "flint_hot"}
	for i, td := range tools {
		if td.Name != want[i] {
			t.Errorf("tools[%d].Name = %q, want %q", i, td.Name, want[i])
		}
		if td.Description == "" {
			t.Errorf("tools[%d].Description empty", i)
		}
		if td.InputSchema == nil {
			t.Errorf("tools[%d].InputSchema nil", i)
		}
	}
}

// TestDispatchReadRoundtrip — flint_read{query,tag,limit} → cli read 조립·출력/코드 전달.
func TestDispatchReadRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"rows":[]}`, 0)
	out, code := DispatchTool("flint_read", map[string]any{
		"query": "인코딩 테스트", "tag": "go", "limit": 3,
	})
	wantArgs(t, calls, []string{"read", "인코딩 테스트", "--tag", "go", "--limit", "3"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != `{"rows":[]}` {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchReadLimitBudget — 토큰 예산: limit 상한 50이 도구에도 적용된다.
func TestDispatchReadLimitBudget(t *testing.T) {
	calls, _ := installRun(t, "", 0)
	DispatchTool("flint_read", map[string]any{"query": "x", "limit": 100})
	if len(*calls) == 0 {
		t.Fatal("runFn not called")
	}
	for i, a := range (*calls)[0].args {
		if a == "--limit" {
			if got := (*calls)[0].args[i+1]; got != "50" {
				t.Errorf("clamped limit = %q, want \"50\"", got)
			}
			return
		}
	}
	t.Error("--limit not passed")
}

// TestDispatchWriteRoundtrip — flint_write{kind,payload} → cli write 조립·stdin JSON.
func TestDispatchWriteRoundtrip(t *testing.T) {
	calls, real := installRun(t, `{"status":"committed","id":1}`, 0)
	out, code := DispatchTool("flint_write", map[string]any{
		"kind": "knowledge",
		"payload": map[string]any{
			"topic": "t", "summary": "s", "confidence": 0.8,
		},
	})
	wantArgs(t, calls, []string{"write", "knowledge"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != `{"status":"committed","id":1}` {
		t.Errorf("out = %q, want fake passthrough", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte((*calls)[0].stdin), &got); err != nil {
		t.Fatalf("stdin not JSON: %v", err)
	}
	if got["topic"] != "t" {
		t.Errorf("stdin topic = %v, want t", got["topic"])
	}
}

// TestDispatchWriteIdempotent — 동일 도구 호출은 동일 CLI 조립(멱등 위임).
func TestDispatchWriteIdempotent(t *testing.T) {
	calls, _ := installRun(t, `{"status":"duplicate"}`, 0)
	args := map[string]any{
		"kind":    "knowledge",
		"payload": map[string]any{"topic": "t", "summary": "s"},
	}
	DispatchTool("flint_write", args)
	DispatchTool("flint_write", args)
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(*calls))
	}
	if !equal((*calls)[0].args, (*calls)[1].args) || (*calls)[0].stdin != (*calls)[1].stdin {
		t.Error("repeat dispatch assembled different CLI args/stdin")
	}
}

// TestDispatchWriteMissingKind — kind 누락은 스키마 오류(exit 3).
func TestDispatchWriteMissingKind(t *testing.T) {
	installRun(t, "", 0)
	out, code := DispatchTool("flint_write", map[string]any{"payload": map[string]any{}})
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
	if !strings.Contains(out, "kind") {
		t.Errorf("out = %q, want kind field error", out)
	}
}

// TestDispatchHotRoundtrip — flint_hot{session_id} → cli hot 조립.
func TestDispatchHotRoundtrip(t *testing.T) {
	calls, real := installRun(t, "hot summary", 0)
	out, code := DispatchTool("flint_hot", map[string]any{"session_id": "s1"})
	wantArgs(t, calls, []string{"hot", "--session", "s1"})
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !real && out != "hot summary" {
		t.Errorf("out = %q, want fake passthrough", out)
	}
}

// TestDispatchUnknownTool — 미정의 도구는 한 줄 에러 JSON·exit 1.
func TestDispatchUnknownTool(t *testing.T) {
	installRun(t, "", 0)
	out, code := DispatchTool("flint_nope", nil)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("out = %q, want unknown tool error", out)
	}
}
