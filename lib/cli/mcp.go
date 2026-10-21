// mcp.go — flint mcp 서브커맨드 등록. 실 서버 루프는 lib/mcp에 있고, 그 패키지가
// cli를 import하므로(tools.go runFn) 여기서는 직접 참조하지 않는다 — init 시점에
// RegisterMCPServer로 주입받은 함수로 위임한다(순환 import 방지).
package cli

import (
	"context"
	"fmt"
	"io"
)

// mcpServe — 주입 시점. lib/mcp init이 RunMCPServer를 넣는다.
var mcpServe func(stdin io.Reader, stdout io.Writer) int

// RegisterMCPServer — lib/mcp가 서버 루프를 주입한다.
func RegisterMCPServer(fn func(stdin io.Reader, stdout io.Writer) int) {
	mcpServe = fn
}

// runMCP — flint mcp: 도구 호출(flint_read 등)은 cli.Run이 각자 store를
// 연결하므로 여기서 받은 st는 쓰지 않는다(nil 허용).
func runMCP(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if mcpServe == nil {
		fmt.Fprintln(stdout, `{"error":"mcp server not linked: build with flint/lib/mcp"}`)
		return 1
	}
	return mcpServe(stdin, stdout)
}

func init() { Register("mcp", runMCP) }
