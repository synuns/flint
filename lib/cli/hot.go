// hot.go — flint hot: 주입용 요약. 형식 고정 + 상한은 meta hot_budget(기본 2,000).
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
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
// 접미 없는 기본 포맷 기준 — --full 접미 포함 산정은 hotLineSurvivorCount.
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

// hotLineSurvivorCount — 행별 '표시 문자열' 기준 생존 수(N4). --full 접미
// (·+N일)가 붙은 최종 텍스트로 잰다: k행 시작 오프셋은 lines[:k]의 rune
// 수(행 구분 줄바꿈 포함)와 같고, 그 오프셋이 budget 미만인 행만 살아남는다.
// ClampHot이 절단하는 실출력과 같은 문자열을 재므로, 접미가 밀어낸 행이
// 실출력에서 잘렸는데도 생존으로 세어 증분되는 부정합이 없다.
func hotLineSurvivorCount(lines []string, budget int) int {
	n, off := 0, 0
	for i, ln := range lines {
		if i > 0 {
			off++ // 행 구분 줄바꿈
		}
		if off >= budget {
			break
		}
		off += utf8.RuneCountInString(ln)
		n++
	}
	return n
}

// injectedIDsForSurvivors — 실제 주입된 지식 id만 동반 질의 결과에서 고른다:
// 생존 survivors행 중 project 팔(id 없음)을 제외한 수만큼 앞에서 취한다.
// hot_view와 동반 질의가 같은 ORDER BY·LIMIT을 쓰므로 k번째 지식 행은
// 동반 질의의 k번째 id와 대응한다(잘린 행의 id는 증분하지 않는다).
func injectedIDsForSurvivors(rows [][]any, ids []int, survivors int) []int {
	k := 0
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

// injectedKnowledgeIDs — hotSurvivorCount(접미 없는 기본 포맷) 기준의
// 증분 id. --full 접미 포함 산정은 runHot이 hotLineSurvivorCount로 한다.
func injectedKnowledgeIDs(rows [][]any, ids []int, budget int) []int {
	return injectedIDsForSurvivors(rows, ids, hotSurvivorCount(rows, budget))
}

// pendingAgeSuffix — pending 팔 대기일수 접미(v0.12.1 R2): valid_at 경과
// 24시간 미만은 빈 문자열, 이상은 내림 일수 "·+N일".
func pendingAgeSuffix(validAt time.Time, now time.Time) string {
	if days := int64(now.Sub(validAt) / (24 * time.Hour)); days > 0 {
		return fmt.Sprintf("·+%d일", days)
	}
	return ""
}

// pendingValidAtsSQL — pending 팔 valid_at 동반 질의(v0.12.1 R2). hot_view
// pending 팔은 ts로 ingested_at을 쓰고 valid_at을 노출하지 않아, HotSQL과
// 동일 조건·정렬(ingested_at DESC, id DESC tie-break)·상한으로 별도 조회한다.
// HotSQL 출력의 k번째 pending 행은 이 질의의 k번째 valid_at과 대응한다.
func pendingValidAtsSQL() string {
	return `SELECT k.valid_at FROM knowledge k
		WHERE k.invalid_at IS NULL AND k.status = 'pending'
		ORDER BY k.ingested_at DESC, k.id DESC LIMIT ` + strconv.Itoa(query.ClampLimit(0))
}

// hotSuffixedLines — --full 전체 출력의 행별 표시 문자열(접미 포함, N4).
// ClampHot 절단 대상이 되는 실출력 텍스트와 증분 생존 판정
// (hotLineSurvivorCount)이 같은 문자열을 기준으로 정합하도록 출력 조립과
// 생존 산정이 공유한다. pending 행 끝에만 대기일수 접미를 붙이고, 동반 질의
// 실패·결과 부족·time.Time 아닌 값·줄바꿈 포함(안전 가드)은 접미 생략해
// 기본 포맷 라인을 반환한다(기존 출력 불변 승계). rows가 비면 nil.
// now는 UTC.
func hotSuffixedLines(ctx context.Context, st Store, rows [][]any, now time.Time) []string {
	base := query.FormatRows(rows, hotFields, false)
	if base == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(base, "\n"), "\n")
	hasPending := false
	for _, r := range rows {
		if len(r) > 0 && fmt.Sprint(r[0]) == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return lines
	}
	vaRows, err := queryOnce(ctx, st, pendingValidAtsSQL(), nil)
	if err != nil {
		return lines
	}
	// 안전 가드: 필드 값에 줄바꿈이 포함되면 FormatRows 한 행이 여러 line으로
	// 쪼개져 lines[i]↔rows[i] 대응이 어긋난다 — 접미가 잘못된 행에 붙지 않도록
	// 행 수가 다르면 접미 전체를 생략한다(기존 출력 불변 승계).
	if len(lines) != len(rows) {
		return lines
	}
	k := 0
	for i, r := range rows {
		if i >= len(lines) {
			break
		}
		if len(r) == 0 || fmt.Sprint(r[0]) != "pending" {
			continue
		}
		if k < len(vaRows) && len(vaRows[k]) > 0 {
			if va, ok := vaRows[k][0].(time.Time); ok {
				lines[i] += pendingAgeSuffix(va, now)
			}
		}
		k++
	}
	return lines
}

// hotFullOutput — 행별 표시 문자열(접미 포함)을 --full 전체 출력 텍스트로
// 조립한다. nil(빈 rows)은 기존 빈 출력("") 승계.
func hotFullOutput(lines []string) string {
	if lines == nil {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// hotHeadline — hot 요약 헤드라인 1줄(R2). minimal은 활성·대기 카운트만,
// 그 외 프로파일(미설정·파손 포함)은 normal 문구(펼치기 안내 포함).
func hotHeadline(profile string, active int, pending int, recent int) string {
	if profile == "minimal" {
		return fmt.Sprintf("[flint] 활성 %d · 대기 %d", active, pending)
	}
	return fmt.Sprintf("[flint] 활성 %d · 대기 %d · 최근 결정 %d — 펼치기: flint hot --full",
		active, pending, recent)
}

// hotCounts — hot_view 조회 행(전체 출력과 동일 데이터 소스)에서
// 헤드라인 카운트를 뽑는다: 활성=projects 팔, 대기=pending, 최근 결정=7일.
func hotCounts(rows [][]any) (active, pending, recent int) {
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		switch fmt.Sprint(r[0]) {
		case "project":
			active++
		case "pending":
			pending++
		case "decision":
			recent++
		}
	}
	return active, pending, recent
}

// runHot — hot_view 조회 후 --full이면 기존 전체 출력(바이트 불변),
// 아니면 프로파일별 헤드라인 1줄만 출력한다(카운트는 동일 조회에서 추출,
// 전체 조립은 생략). 계측·exit·--session은 양 경로 공통 불변.
// T38 배선(F-T56-1): 시작 시 meta hot_stats 누적을 로드하고 종료 직전
// persist한다(단발 CLI 프로세스 분리 보존). 둘 다 계측이므로 실패는
// 조용히 무시된다(§13 — hot 출력·exit에 영향 금지).
func runHot(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	full := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
		}
		if args[i] == "--full" {
			full = true
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
	// 예산은 양 경로 공통 산출: --full 절단 규칙과 증분 생존 판정이 같은
	// 예산을 공유한다(계측 불변).
	budget := HotBudget(metaGetKV(ctx, st))
	// 접미 포함 행별 텍스트(N4) — --full 출력과 증분 생존 판정이 공유한다.
	var lines []string
	if full {
		lines = hotSuffixedLines(ctx, st, rows, time.Now().UTC())
		clamped := query.ClampHot(hotFullOutput(lines), budget)
		fmt.Fprint(stdout, clamped)
		RecordHotLen(len([]rune(clamped)))
	} else {
		kv := metaGetKV(ctx, st)
		profile := ExposureProfile(func(key string) (string, error) { return kv(key, "") })
		active, pending, recent := hotCounts(rows)
		line := hotHeadline(profile, active, pending, recent)
		fmt.Fprintln(stdout, line)
		RecordHotLen(len([]rune(line)))
	}
	persistHotStatsToMeta(ctx, st)
	// 계측(T62): 실제 주입된(예산 절단 후 출력에 남은) 지식 id만 참조 증분
	// + hot 유입 1회. 조용히(§13). 지식 주입은 --full 전체 출력 경로에서만
	// 일어난다(N3) — 헤드라인 경로는 카운트만 노출하므로 실제 주입 없는
	// 증분(read_count·last_read_at 왜곡으로 stale 판정 동결) 없이 유입 1회만
	// 기록한다. 생존 판정은 접미 포함 텍스트 기준(N4): 접미가 밀어낸 행이
	// 실출력에서 잘렸으면 증분하지 않는다. 동반 질의 실패 시 BumpReads 호출
	// 자체를 건너뛴다 — 유입 1회 기록은 조회 성공 사실과 무관하므로 유지한다.
	if !full {
		recordUsage(ctx, st, "hot", "")
	} else if idRows, err := queryOnce(ctx, st, hotKnowledgeIDsSQL(), nil); err == nil {
		instrumentReads(ctx, st, "hot",
			injectedIDsForSurvivors(rows, rowIDs(idRows), hotLineSurvivorCount(lines, budget)))
	} else {
		recordUsage(ctx, st, "hot", "")
	}
	return 0
}
