// flint — 단일 바이너리 진입점. thin wrapper: 인자·표준입출력을 cli.Run에
// 넘기고 exit code로 종료한다(로직은 lib/cli에 있다).
package main

import (
	"os"

	"flint/lib/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout))
}
