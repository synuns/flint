package main

import (
	"os"

	"github.com/synuns/flint/lib/cli"

	_ "github.com/synuns/flint/lib/mcp"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout))
}
