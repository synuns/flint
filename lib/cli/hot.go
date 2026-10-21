// hot.go — flint hot: 주입용 요약. 형식 고정 + 2,000자 상한(토큰 예산).
package cli

import (
	"context"
	"fmt"
	"io"

	"flint/lib/query"
	"flint/lib/store"
)

// hotMax — hot 출력 임시 상한(2주 실측 p95 후 확정).
const hotMax = 2000

// runHot — hot_view 조회 후 고정 포맷으로 출력하고 상한으로 절단한다.
func runHot(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
		}
	}
	sql := query.HotSQL(session)
	rows, err := queryOnce(ctx, st, sql, nil)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	out := query.FormatRows(rows, "kind,topic,summary,ts", false)
	fmt.Fprint(stdout, query.ClampHot(out, hotMax))
	return 0
}
