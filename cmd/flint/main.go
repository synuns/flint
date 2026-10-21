// flint — 단일 바이너리 진입점. thin wrapper: 인자·표준입출력을 cli.Run에
// 넘기고 exit code로 종료한다(로직은 lib/cli에 있다).
package main

import (
	"os"

	"flint/lib/cli"

	// blank import — lib/mcp는 cli를 import하므로(tools.go runFn) 역방향
	// 참조가 불가하다. 여기서 링크해야 mcp init이 RunMCPServer를 주입하고
	// `flint mcp`가 동작한다(T49/T57 배선).
	_ "flint/lib/mcp"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout))
}
