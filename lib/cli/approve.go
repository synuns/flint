// approve 커맨드 — 검수 게이트 통과(§8: 승인 = 대화로 → 에이전트가 실행).
// `flint approve --id N` — pending→active 승격(T17). 성공 {"status":"approved","id":N}
// exit 0, 없는 id는 not_found 한 줄 JSON exit 1.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"flint/lib/db"
	"flint/lib/store"
)

// approveFn — seam. 기본 T17 실구현(자체 Repo open — Store 인터페이스에
// 갱신 경로가 없어 backup과 동일 방식), 테스트는 fake 교체(SUPERLITE_REAL=1은 위임).
var approveFn = func(ctx context.Context, id int) error {
	repo, err := db.NewRepo(ctx, dsnFromEnv())
	if err != nil {
		return err
	}
	defer repo.Close()
	return repo.Approve(ctx, id)
}

// runApprove — 승인 실행. st는 root 디스패치 호환용이며 사용하지 않는다.
func runApprove(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	id := -1
	for i := 0; i < len(args); i++ {
		if args[i] != "--id" {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
		i++
		if i >= len(args) {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("--id requires a number")))
			return 1
		}
		n, err := strconv.Atoi(args[i])
		if err != nil || n < 1 {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("--id: invalid id %q", args[i])))
			return 1
		}
		id = n
	}
	if id < 1 {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("approve requires --id")))
		return 1
	}

	if err := approveFn(ctx, id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fmt.Fprintln(stdout, `{"error":"not_found","id":`+strconv.Itoa(id)+`}`)
			return 1
		}
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+`}`)
	return 0
}

func init() { Register("approve", runApprove) }
