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
	// 계측(T62): 주입 지식 id 전수 참조 증분 + hot 유입 1회. 조용히(§13).
	// 동반 질의 실패 시 BumpReads 호출 자체를 건너뛴다 — 유입 1회 기록은
	// 조회 성공 사실과 무관하므로 유지한다.
	if idRows, err := queryOnce(ctx, st, hotKnowledgeIDsSQL(), nil); err == nil {
		instrumentReads(ctx, st, "hot", rowIDs(idRows))
	} else if us, ok := st.(usageRecorder); ok {
		_ = us.RecordUsage(ctx, "hot", "")
	}
	return 0
}
