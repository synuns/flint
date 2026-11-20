// mcp.go — registers the flint mcp subcommand. The actual server loop lives in lib/mcp; that package
// imports cli (tools.go runFn), so it is not referenced directly here — at init time we
// delegate via the function injected by RegisterMCPServer (avoids an import cycle).
package cli

import (
	"context"
	"fmt"
	"io"
)

// mcpServe — injection point. lib/mcp init installs RunMCPServer.
var mcpServe func(stdin io.Reader, stdout io.Writer) int

// RegisterMCPServer — lib/mcp injects the server loop.
func RegisterMCPServer(fn func(stdin io.Reader, stdout io.Writer) int) {
	mcpServe = fn
}

// runMCP — flint mcp: tool calls (flint_read etc.) connect their own store in
// cli.Run, so the st received here is unused (nil allowed).
func runMCP(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if mcpServe == nil {
		fmt.Fprintln(stdout, `{"error":"mcp server not linked: build with flint/lib/mcp"}`)
		return 1
	}
	return mcpServe(stdin, stdout)
}

func init() { Register("mcp", runMCP) }
