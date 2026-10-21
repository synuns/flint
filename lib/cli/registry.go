// registry — 서브커맨드 등록 테이블. root.go의 switch를 대체해
// 신규 커맨드가 자기 파일에서 Register만 하면 되게 한다(개방-폐쇄, v0.1 부채).
package cli

import (
	"context"
	"io"
)

// Handler — 서브커맨드 실행 함수. Run이 store 접속 후 호출한다.
type Handler func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int

// registry — 커맨드명 → 핸들러. 등록은 init/시작 시점에만 일어나 잠금 불필요.
var registry = map[string]Handler{}

// Register — 커맨드 등록. 같은 이름 재등록은 최신값으로 덮어쓴다(테스트 교체용).
func Register(name string, h Handler) {
	registry[name] = h
}

// Lookup — 등록된 핸들러 조회. 미등록이면 false.
func Lookup(name string) (Handler, bool) {
	h, ok := registry[name]
	return h, ok
}

// unregister — 테스트 정리용(프로덕션 경로 아님).
func unregister(name string) {
	delete(registry, name)
}

// usageLine — 인자 없음/도움말 공용 한 줄 JSON.
func usageLine() string {
	return `{"error":"usage: flint <write|read|hot|status|backup|restore|approve|maintain|done|version> [args]"}`
}

func init() {
	Register("write", runWrite)
	Register("read", runRead)
	Register("hot", runHot)
	Register("status", runStatus)
	Register("backup", runBackup)
	Register("restore", runRestore)
	// help는 미등록 — Run이 store 접속 전에 출력하는 usage 경로가 그대로 담당한다.
}
