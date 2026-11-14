// run_store.go — v1.2.4 T1(B4): store 주입 실행 변형. flint mcp 장기
// 프로세스가 도구 호출마다 store를 다시 열지 않도록 RunWithStore로
// 서버 수명 store를 주입한다. 기존 cli.Run(root.go)은 무변 — nil 주입은
// wire.Run(newStore) 기존 경로와 동치다.
package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/wire"
)

// injectedStore — 주입 store의 Close 차단 래퍼. wire.Run은 명령 완료 후
// st.Close()하는 단발 CLI 계약(v0.25.3 누수 패치)이라 서버 수명 store를
// 그대로 넘기면 첫 도구 호출에 닫힌다. 위임은 전부 원본, Close만 no-op.
type injectedStore struct{ wire.Store }

func (injectedStore) Close() {}

// OpenServerStore — 서버 수명 store open(DSN 해결 포함). lib/mcp
// storeSupply 기본 실구현이 쓴다. 실패는 호출자가 nil 폴백으로 흡수
// (각 호출이 기존 Run 계약대로 알아서 실패한다).
func OpenServerStore() (wire.Store, error) {
	return newStore(context.Background(), wire.DSNFromEnv())
}

// RunWithStore — 주입 store로 커맨드를 실행한다. st가 nil이면 기존
// Run과 동일하게 내부 open(wire.Run + cli.newStore seam) — 출력·exit
// 계약 불변. 주입 시 연결 수명은 pgx pool에 위임(재연결 포함)하고
// Close만 차단해 서버 수명 재사용을 보장한다.
func RunWithStore(args []string, stdin io.Reader, stdout io.Writer, st wire.Store) int {
	if st == nil {
		return wire.Run(args, stdin, stdout, newStore)
	}
	return wire.Run(args, stdin, stdout, func(ctx context.Context, dsn string) (wire.Store, error) {
		return injectedStore{st}, nil
	})
}
