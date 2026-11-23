package cli

import (
	"context"
	"fmt"
	"io"
)

var mcpServe func(stdin io.Reader, stdout io.Writer) int

func RegisterMCPServer(fn func(stdin io.Reader, stdout io.Writer) int) {
	mcpServe = fn
}

func runMCP(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if mcpServe == nil {
		fmt.Fprintln(stdout, `{"error":"mcp server not linked: build with flint/lib/mcp"}`)
		return 1
	}
	return mcpServe(stdin, stdout)
}

func init() { Register("mcp", runMCP) }
