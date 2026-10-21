// hot.go — flint hot: 주입용 요약. 형식 고정 + 상한은 meta hot_budget(기본 2,000).
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"flint/lib/query"
	"flint/lib/store"
)

// defaultHotBudget — meta hot_budget 미설정 시 기본 상한(TUNING.md §1).
const defaultHotBudget = 2000

// HotBudget — meta hot_budget 우선, 미설정·무효 값은 기본 2,000(에러 아님).
func HotBudget(getKV func(string, string) (string, error)) int {
	v, err := getKV("hot_budget", "")
	if err != nil || v == "" {
		return defaultHotBudget
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return defaultHotBudget
	}
	return n
}

// metaGetKV — meta 키 조회를 getKV 형태로 감싼다(미설정·오류는 def).
func metaGetKV(ctx context.Context, st Store) func(string, string) (string, error) {
	return func(key, def string) (string, error) {
		rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
		if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
			return def, err
		}
		return fmt.Sprint(rows[0][0]), nil
	}
}

// runHot — hot_view 조회 후 고정 포맷으로 출력하고 상한으로 절단한다.
// T38 배선(F-T56-1): 시작 시 meta hot_stats 누적을 로드하고 종료 직전
// persist한다(단발 CLI 프로세스 분리 보존). 둘 다 계측이므로 실패는
// 조용히 무시된다(§13 — hot 출력·exit에 영향 금지).
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
	// 조회 성공 후 기록 직전에 baseline 로드(쿼리 실패 경로는 계측 생략).
	loadHotStatsFromMeta(ctx, st)
	out := query.FormatRows(rows, "kind,topic,summary,ts", false)
	clamped := query.ClampHot(out, HotBudget(metaGetKV(ctx, st)))
	fmt.Fprint(stdout, clamped)
	RecordHotLen(len([]rune(clamped)))
	persistHotStatsToMeta(ctx, st)
	return 0
}
