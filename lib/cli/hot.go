// hot.go — flint hot: 주입용 요약. 형식 고정 + 상한은 meta hot_budget(기본 2,000).
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

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

// hotKnowledgeIDsSQL — 실제 주입된 지식의 id 전수(참조 계측용 동반 질의).
// 뷰의 세 팔(활성 프로젝트·pending·최근 결정 7일)을 동일 조건으로 다시
// 모은 뒤, HotSQL과 같은 ORDER BY ts DESC, id DESC LIMIT으로 상위 N행을
// 먼저 가른다. tie-break ", id DESC"(CI 39713526850 룰링): 동률 ts에서
// 플래너 임의 순서로는 주입 집합과 이 증분 집합이 어긋난다 — hot_view가
// 말미에 노출하는 id와 동일한 완전 순서로 주입 집합=증분 집합을 보장한다.
// 프로젝트 팔은 knowledge 행이 아니어서(read_count 없음) id NULL로 순위를
// 메운 뒤 제외된다 — 주입되지도 않은 지식(상한 밀려난 항목)까지 증분하는
// 이중 과다 계측을 막는다.
func hotKnowledgeIDsSQL() string {
	return `SELECT id FROM (
		SELECT id, ts FROM (
			SELECT NULL::int AS id, p.updated AS ts
			FROM projects p WHERE p.status = 'active'
			UNION ALL
			SELECT k.id, k.ingested_at FROM knowledge k
			WHERE k.invalid_at IS NULL AND k.status = 'pending'
			UNION ALL
			SELECT k.id, k.valid_at FROM knowledge k
			WHERE k.invalid_at IS NULL
			  AND k.ingested_at >= now() - interval '7 days'
			  AND k.domain = 'decision'
		) u ORDER BY ts DESC, id DESC LIMIT ` + strconv.Itoa(query.ClampLimit(0)) + `
	) top WHERE id IS NOT NULL`
}

// hotFields — hot 출력 컬럼(포맷·예산 생존 판정이 공유하는 고정 순서).
const hotFields = "kind,topic,summary,ts"

// hotSurvivorCount — ClampHot(budget) 절단 뒤에도 출력에 남은 rows 행 수.
// FormatRows는 행독립(헤더 없음)이라 k행의 시작 오프셋은 rows[:k] 포맷
// 결과의 rune 수와 같다. 시작 오프셋이 budget 미만인 행만 살아남는다
// (경계에 걸려 부분 노출된 행도 노출분은 주입된 것으로 본다).
func hotSurvivorCount(rows [][]any, budget int) int {
	n := 0
	for i := range rows {
		if utf8.RuneCountInString(query.FormatRows(rows[:i], hotFields, false)) >= budget {
			break
		}
		n++
	}
	return n
}

// injectedKnowledgeIDs — 실제 주입된 지식 id만 동반 질의 결과에서 고른다:
// 예산 생존 행 중 project 팔(id 없음)을 제외한 수만큼 앞에서 취한다.
// hot_view와 동반 질의가 같은 ORDER BY·LIMIT을 쓰므로 k번째 지식 행은
// 동반 질의의 k번째 id와 대응한다(잘린 행의 id는 증분하지 않는다).
func injectedKnowledgeIDs(rows [][]any, ids []int, budget int) []int {
	k := 0
	survivors := hotSurvivorCount(rows, budget)
	for i := 0; i < survivors && i < len(rows); i++ {
		if len(rows[i]) == 0 || fmt.Sprint(rows[i][0]) != "project" {
			k++
		}
	}
	if k > len(ids) {
		k = len(ids)
	}
	return ids[:k]
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
	out := query.FormatRows(rows, hotFields, false)
	budget := HotBudget(metaGetKV(ctx, st))
	clamped := query.ClampHot(out, budget)
	fmt.Fprint(stdout, clamped)
	RecordHotLen(len([]rune(clamped)))
	persistHotStatsToMeta(ctx, st)
	// 계측(T62): 실제 주입된(예산 절단 후 출력에 남은) 지식 id만 참조 증분
	// + hot 유입 1회. 조용히(§13). 동반 질의 실패 시 BumpReads 호출 자체를
	// 건너뛴다 — 유입 1회 기록은 조회 성공 사실과 무관하므로 유지한다.
	if idRows, err := queryOnce(ctx, st, hotKnowledgeIDsSQL(), nil); err == nil {
		instrumentReads(ctx, st, "hot", injectedKnowledgeIDs(rows, rowIDs(idRows), budget))
	} else {
		recordUsage(ctx, st, "hot", "")
	}
	return 0
}
