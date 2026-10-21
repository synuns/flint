// serve.go — flint mcp 진입. Serve(JSON-RPC 루프)와 DispatchTool(도구 3종)를
// 잇는다: initialize → tools/list → tools/call 순서 응답, EOF 정상 종료 0.
// lib/mcp는 cli를 import하므로(tools.go runFn), 역방향 참조는 cli가 노출하는
// RegisterMCPServer 주입 시점으로 해결한다(순환 import 방지).
package mcp

import (
	"encoding/json"
	"io"

	"flint/lib/cli"
)

// serverInfoMeta — cli.VersionString(ldflags 주입)을 초기화 응답에 녹인다.
var serverInfoMeta = func() map[string]any {
	m := map[string]any{"name": "flint", "version": "dev"}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cli.VersionString()), &parsed); err == nil {
		if v, ok := parsed["version"].(string); ok {
			m["version"] = v
		}
	}
	return m
}()

// handleRPC — JSON-RPC 메서드 3종 + 미정의(-32601). tools/call은 DispatchTool로
// 위임해 CLI 계약(exit 0/1/2/3·한 줄 JSON)을 도구 경로에도 그대로 적용한다.
func handleRPC(method string, params map[string]any) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      serverInfoMeta,
		}, nil
	case "tools/list":
		return map[string]any{"tools": ToolsManifest()}, nil
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		out, code := DispatchTool(name, args)
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": out}},
			"isError": code != 0,
		}, nil
	default:
		return nil, ErrUnknownMethod
	}
}

// RunMCPServer — stdin의 줄단위 JSON-RPC를 처리해 stdout에 한 줄 응답을 쓴다.
// EOF는 정상 종료(0). Serve 자체 오류(쓰기 실패 등)만 1.
func RunMCPServer(stdin io.Reader, stdout io.Writer) int {
	if err := Serve(stdin, stdout, handleRPC); err != nil {
		return 1
	}
	return 0
}

func init() { cli.RegisterMCPServer(RunMCPServer) }
