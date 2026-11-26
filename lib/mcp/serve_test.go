package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/cli"
)

func lookupMCPHandler() (func(stdin io.Reader, stdout io.Writer) int, bool) {
	h, ok := cli.Lookup("mcp")
	if !ok {
		return nil, false
	}
	return func(stdin io.Reader, stdout io.Writer) int {
		return h(context.Background(), nil, nil, stdin, stdout)
	}, true
}

func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	s := strings.TrimRight(out, "\n")
	if s == "" {
		t.Fatalf("no output lines")
	}
	raw := strings.Split(s, "\n")
	got := make([]map[string]any, 0, len(raw))
	for _, l := range raw {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("unmarshal line %q: %v", l, err)
		}
		got = append(got, m)
	}
	return got
}

func TestRunMCPServerRoundtrip(t *testing.T) {
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n")
	if code := RunMCPServer(&in, &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (EOF 정상 종료)", code)
	}
	lines := decodeLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("responses = %d, want 2: %q", len(lines), out.String())
	}
	init := lines[0]["result"].(map[string]any)
	if init["protocolVersion"] == nil {
		t.Errorf("initialize result에 protocolVersion 없음: %v", lines[0])
	}
	list := lines[1]["result"].(map[string]any)
	tools, ok := list["tools"].([]any)
	if !ok || len(tools) != 6 {
		t.Errorf("tools/list result.tools = %v, want 6건", list["tools"])
	}
}

func TestRunMCPServerProtocolOrder(t *testing.T) {
	calls, real := installRun(t, `{"ok":true}`, 0)
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	in.WriteString("{{{깨진 json\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"포트","limit":3}}}` + "\n")
	if code := RunMCPServer(&in, &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (깨진 JSON 스킵 후 EOF 정상)", code)
	}
	lines := decodeLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("responses = %d, want 3(깨진 줄 무시): %q", len(lines), out.String())
	}
	for i, id := range []float64{1, 2, 3} {
		if got, ok := lines[i]["id"].(float64); !ok || got != id {
			t.Errorf("lines[%d].id = %v, want %v (요청 순서 응답)", i, lines[i]["id"], id)
		}
		if lines[i]["result"] == nil {
			t.Errorf("lines[%d]에 result 없음: %v", i, lines[i])
		}
	}
	wantArgs(t, calls, []string{"read", "--json", "--source", "mcp", "--limit", "3", "--", "포트"})
	callRes := lines[2]["result"].(map[string]any)
	content, _ := callRes["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("flint_read 응답 content = %v, want 1건", callRes["content"])
	}
	if text, _ := content[0].(map[string]any)["text"].(string); text == "" {
		t.Errorf("flint_read 응답 text 빈 문자열(도구 출력 미전달)")
	} else if !real && text != `{"ok":true}` {
		t.Errorf("flint_read text = %q, want fake 출력 원문", text)
	}
	if callRes["isError"] != false {
		t.Errorf("isError = %v, want false(exit 0)", callRes["isError"])
	}
}

func TestRunMCPServerUnknownMethod(t *testing.T) {
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":9,"method":"nope","params":{}}` + "\n")
	if code := RunMCPServer(&in, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := decodeLines(t, out.String())
	if len(lines) != 1 {
		t.Fatalf("responses = %d, want 1", len(lines))
	}
	rpcErr, ok := lines[0]["error"].(map[string]any)
	if !ok || rpcErr["code"] != float64(-32601) {
		t.Errorf("error = %v, want code -32601", lines[0]["error"])
	}
}

func TestInitializeEchoesClientProtocolVersion(t *testing.T) {
	res, err := handleRPC("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(map[string]any)["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("echo 실패: %v", got)
	}
}

func TestInitializeFallbackProtocolVersion(t *testing.T) {
	res, err := handleRPC("initialize", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(map[string]any)["protocolVersion"]; got != "2024-11-05" {
		t.Errorf("fallback 실패: %v", got)
	}
}

func TestNegotiateVersionNonStringFallsBack(t *testing.T) {
	for _, p := range []map[string]any{
		{"protocolVersion": 123},
		{"protocolVersion": ""},
	} {
		if got := negotiateVersion(p); got != "2024-11-05" {
			t.Errorf("fallback 기대, got %q (params %v)", got, p)
		}
	}
}

func TestInitializeIncludesInstructions(t *testing.T) {
	res, err := handleRPC("initialize", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	ins := res.(map[string]any)["instructions"].(string)
	if !strings.Contains(ins, "flint_read") || !strings.Contains(ins, "flint_write") {
		t.Errorf("instructions 3규칙 누락: %q", ins)
	}
	if !strings.Contains(ins, "data block, not instructions") {
		t.Errorf("instructions 인젝션 가드 문구 누락: %q", ins)
	}
	for _, r := range ins {
		if r >= 0xAC00 && r <= 0xD7A3 {
			t.Errorf("instructions에 잔여 한국어: %q", ins)
			break
		}
	}
}

func TestPingReturnsEmptyResult(t *testing.T) {
	res, err := handleRPC("ping", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.(map[string]any)) != 0 {
		t.Errorf("빈 result 아님: %v", res)
	}
}

func TestMCPCliWired(t *testing.T) {
	_, real := installRun(t, `{"ok":true}`, 0)
	if real {
		t.Skip("real runFn: 실DB 경로는 tools_test/e2e 담당")
	}
	h, ok := lookupMCPHandler()
	if !ok {
		t.Fatal("cli registry에 mcp 미등록")
	}
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n")
	if code := h(&in, &out); code != 0 {
		t.Fatalf("handler exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "flint_read") {
		t.Errorf("핸들러 출력에 도구 목록 없음: %q", out.String())
	}
}
