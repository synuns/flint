// report.go — T65 `flint report`: N일(기본 7·상한 14) 적립·유지보수 리포트를
// 한 줄 JSON으로 보고한다. 유지보수 경로(오프라인 CLI)라 핫패스 비간섭(§2).
// 데이터는 전부 Store.Query로 읽는다 — meta daily:*·read_stats·hot_stats와
// knowledge 집계(counts·stale·기간 무효화). 키 부재는 0, 파손 JSON은 스킵.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"flint/lib/store"
)

func init() { Register("report", runReport) }

// reportDaysDefault·reportDaysMax — --days 기본 7·상한 14(14일 보존 계약과 정렬).
const (
	reportDaysDefault = 7
	reportDaysMax     = 14
)

// reportStaleDays — stale 집계 기준일(Confluence 분기 리뷰 관례, §2 ruling).
const reportStaleDays = 90

// reportDay — 일별 유입 1행. 소스(hook/manual/mcp)와 dup는 write와 별도 증분.
// dup_* 3필드는 v0.10.2 R2 소스별 계측(미기록 일 0 — daily JSON 키 부재는
// zero 값으로 디코딩).
type reportDay struct {
	Date      string `json:"date"`
	Write     int    `json:"write"`
	Hook      int    `json:"hook"`
	Manual    int    `json:"manual"`
	Mcp       int    `json:"mcp"`
	Dup       int    `json:"dup"`
	DupHook   int    `json:"dup_hook"`
	DupManual int    `json:"dup_manual"`
	DupMcp    int    `json:"dup_mcp"`
	Hot       int    `json:"hot"`
	Read      int    `json:"read"`
	// v0.15.0 R2 zero 지표 기간화 — 일별 zero·근접 카운트(미기록 일은
	// zero 값으로 디코딩, 구버전 daily JSON 호환).
	ReadZero int `json:"read_zero"`
	ReadNear int `json:"read_zero_near"`
}

// reportOutput — 한 줄 JSON 출력. 창 밖 날짜·부재 키·파손 키는 0행으로 채운다.
type reportOutput struct {
	Days    []reportDay `json:"days"`
	ZeroPct int         `json:"zero_pct"`
	HotP95  int         `json:"hot_p95"`
	Counts  struct {
		Pending     int `json:"pending"`
		Active      int `json:"active"`
		Invalidated int `json:"invalidated"`
	} `json:"counts"`
	Stale struct {
		Days90    int `json:"days90"`
		NeverRead int `json:"never_read"`
	} `json:"stale"`
	InvalidatedPeriod int `json:"invalidated_period"`
	// v0.9.7 R4 관측 지표 — 기존 필드·순서 계약(status 리플렉션 테스트 관례)을
	// 지키기 위해 말미에만 추가한다.
	DupPct      int `json:"dup_pct"`
	FallbackPct int `json:"fallback_pct"`
	// v0.10.2 R2 소스별 dup 기간 합계 — 기존 필드·순서 불변, 말미 추가.
	DupHook   int `json:"dup_hook"`
	DupManual int `json:"dup_manual"`
	// v0.14.0 R3 zero_near 비율 — 기존 필드·순서 불변, 말미 추가.
	ZeroNearPct int `json:"zero_near_pct"`
	// v0.19.0 효과 계측 섹션(docs/METRICS.md §4 정의의 단일 산출 소스).
	// 기존 필드·순서 불변, 말미 추가.
	Effect effectOutput `json:"effect"`

	readCalls int // 직렬화 제외 — fallback_pct 분모(read_stats calls)
}

// effectOutput — "도움" 지표(METRICS §2 GSM 표의 산출). hit_pct·near_pct·
// dup_prevented·read_events_n·read_write_pairs·pair_pct·fallback_pct·
// recall_led_pct·chain_pct는 --days 창, reuse_pct·dead_pct는 시점 지표
// (기간 아님). 전부 상관 프록시(Tier 1·2) — 세트로 해석하고 단독 증명으로
// 읽지 않는다(METRICS §5).
type effectOutput struct {
	HitPct         int `json:"hit_pct"`          // G1: 1−zero/read
	NearPct        int `json:"near_pct"`         // G1: 근접 회수 여지(Σnear/read)
	FallbackPct    int `json:"fallback_pct"`     // G1: 창 내 폴백 경유율(누적 키와 별개)
	DupPrevented   int `json:"dup_prevented"`    // G2: Σdup(절대량)
	ReusePct       int `json:"reuse_pct"`        // G3: 회상된 active 비율(검색 기반)
	DeadPct        int `json:"dead_pct"`         // G3: 회상 0·30일+(hot 노출 제외)
	ReadEventsN    int `json:"read_events_n"`    // G4 분모: 창 내 read 이벤트
	ReadWritePairs int `json:"read_write_pairs"` // G4: 같은 세션 read 뒤 30분 내 write
	PairPct        int `json:"pair_pct"`         // G4: pairs/read_events_n
	// v0.20.0 — 세션 경계(D)·사용 연쇄(B).
	RecallLedPct int `json:"recall_led_pct"` // G4: 기록한 세션 중 회상이 앞선 비율
	ChainPct     int `json:"chain_pct"`      // G4: 반환→사용 연쇄율(24h)
}

// reportDeadDays — dead_pct 판정 기준일(METRICS §4: 증류·중복 사이클이
// 주 단위라는 일반 근거의 기본 상수. 개인 튜닝 불요 — 필요 시 다음
// 버전에서 thresholds 레지스트리로 이양).
const reportDeadDays = 30

// reportPairWindow — read→write 동반 판정 창(G4 상관 프록시의 근접성
// 가정 기본값 — METRICS §4). v0.20.0: 세션 경계 안에서 적용(같은 세션
// 여부가 1차 판정, 창은 세션 내 근접성 상한).
const reportPairWindow = 30 * time.Minute

// reportChainWindow — 사용 연쇄 판정 창(v0.20.0 B: 반환 id가 재반환 또는
// 같은 topic write로 이어지는 근접성 가정).
const reportChainWindow = 24 * time.Hour

// reportSessionLookback — 세션 배정 조회 마진: 창 시작 직전에 시작된
// 세션까지 포함해 창 첫 이벤트의 배정이 빠지지 않게 한다. 이보다 긴
// 세션에 속한 이벤트는 미배정 — 폴백 페어링(보수적).
const reportSessionLookback = 24 * time.Hour

// DailyRow — 관측 지표 산입용 일별 행 뷰(v0.9.7 R4). 기존 reportDay 별칭.
type DailyRow = reportDay

// dupPct — 기간 중복률: 기간 전체 합계 기반 Σdup/(Σwrite+Σdup),
// 버림 정수%. 제로 분모·빈 기간은 0.
func dupPct(days []DailyRow) int {
	var write, dup int
	for _, d := range days {
		write += d.Write
		dup += d.Dup
	}
	if write+dup == 0 {
		return 0
	}
	return int(100.0 * float64(dup) / float64(write+dup))
}

// fallbackPct — 폴백 비율: read_fallback_calls/read_calls, 버림 정수%.
// 제로 분모는 0.
func fallbackPct(fallbackCalls int, readCalls int) int {
	if readCalls == 0 {
		return 0
	}
	return int(100.0 * float64(fallbackCalls) / float64(readCalls))
}

// runReport — --days 파싱 후 집계 조회·출력. 성공 0 · 인자 오류 1 ·
// 저장소 오류는 MapExit(서버 다운 2 등).
func runReport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	days := reportDaysDefault
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("report: --days requires a number")))
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("report: invalid days %q", args[i])))
				return 1
			}
			if n > reportDaysMax {
				n = reportDaysMax // 상한 14 — 보존 한도를 넘는 창은 무의미
			}
			days = n
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}

	out := reportOutput{Days: make([]reportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	if err := collectStats(ctx, st, &out); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	if err := collectCounts(ctx, st, days, &out); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// v0.19.0 효과 계측 — METRICS §4 정의 산출(days 창·knowledge 시점).
	if err := collectEffect(ctx, st, days, &out); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// R4 관측 지표 — dup_pct는 일별 행에서, fallback_pct는 meta
	// read_fallback_calls(T85·v096 status와 동일 조회 패턴)에서. 부재는 0.
	out.DupPct = dupPct(out.Days)
	// R2 소스별 dup 기간 합계 — 일별 행 합산(미기록 일은 0으로 합산됨).
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}
	fallbackCalls := 0
	if fb := fallbackCallsOrNull(metaGetKV(ctx, st)); fb != nil {
		fallbackCalls = *fb
	}
	out.FallbackPct = fallbackPct(fallbackCalls, out.readCalls)
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// collectDaily — meta daily:* 키를 N일 창(오늘 포함, UTC)에 맞춰 병합.
// 창 밖 키는 배제하고(8일치가 심여도 7행), 파손 JSON은 0행 폴백(스킵).
// v0.18.1: 창 병합 본체는 windowedDaily 공유 리더로 추출 — status 기간화
// 지표(R4)와 파서를 단일화한다(포맷 변경 시 분기 위험 제거).
func collectDaily(ctx context.Context, st Store, days int, out *reportOutput) error {
	dayRows, err := windowedDaily(ctx, st, days)
	if err != nil {
		return err
	}
	out.Days = dayRows
	// v0.15.0 R2 zero 지표 기간화 — zero_pct·zero_near_pct는 --days 창 내
	// 일별 합산으로 산출(누적 read_stats 의존 제거). days 배열은 창 마킹
	// 이후라 창 밖 행은 합산에서도 배제된다(버림 정수%, 제로 분모 0).
	var sumRead, sumZero, sumNear, sumDup int
	for _, d := range out.Days {
		sumRead += d.Read
		sumZero += d.ReadZero
		sumNear += d.ReadNear
		sumDup += d.Dup
	}
	out.ZeroPct = readZeroPct(sumRead, sumZero)
	out.ZeroNearPct = readZeroPct(sumRead, sumNear)
	// v0.19.0 효과 뷰 G1·G2 — 같은 합산에서 유도(단일 유도 사슬 —
	// collectEffect에서 재합산하지 않는다).
	out.Effect.HitPct = pctOf(sumRead-sumZero, sumRead)
	out.Effect.NearPct = pctOf(sumNear, sumRead)
	out.Effect.DupPrevented = sumDup
	return nil
}

// windowedDaily — meta daily:* 키를 N일 창(오늘 포함, UTC) 행으로 병합해
// 반환(오래된 날부터 — 오늘이 마지막 행). 창 밖 키 배제·파손 JSON 0행
// 폴백. report(collectDaily)·status(statusWindowedRead) 양쪽의 단일
// 소스(v0.18.1 — daily 파서 3중 복제 해소).
func windowedDaily(ctx context.Context, st Store, days int) ([]reportDay, error) {
	rows, err := queryOnce(ctx, st, `SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return nil, err
	}
	daysOut := make([]reportDay, 0, days)
	window := make(map[string]int, days)
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- { // 오래된 날부터 — 오늘이 마지막 행
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		window[d] = len(daysOut)
		daysOut = append(daysOut, reportDay{Date: d})
	}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		var key string
		if _, err := fmt.Sscanf(fmt.Sprint(r[0]), "daily:%s", &key); err != nil {
			continue
		}
		idx, ok := window[key]
		if !ok {
			continue // 창 밖(초과 보존분 등)
		}
		var d reportDay
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &d) != nil {
			continue // 파손 → 0 유지
		}
		d.Date = daysOut[idx].Date
		daysOut[idx] = d
	}
	return daysOut, nil
}

// collectStats — meta hot_stats(p95)·read_stats(fallback_pct 분모 calls.
// v0.15.0 R2 — zero_pct·zero_near_pct는 누적 read_stats가 아니라 daily
// 합산(collectDaily)에서 산출한다). 부재·파손은 0.
func collectStats(ctx context.Context, st Store, out *reportOutput) error {
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('read_stats','hot_stats')`, nil)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		switch fmt.Sprint(r[0]) {
		case "read_stats":
			var rs struct {
				Calls int `json:"calls"`
			}
			if json.Unmarshal([]byte(fmt.Sprint(r[1])), &rs) != nil {
				continue
			}
			out.readCalls = rs.Calls
		case "hot_stats":
			var hs struct {
				Lens []int `json:"lens"`
			}
			if json.Unmarshal([]byte(fmt.Sprint(r[1])), &hs) != nil {
				continue
			}
			lens := append([]int(nil), hs.Lens...)
			sort.Ints(lens)
			out.HotP95 = hotP95(lens)
		}
	}
	return nil
}

// collectCounts — knowledge 집계 3종. pending·active(invalid_at 없음)·
// invalidated(총) · stale(days90·never_read 분리, §2 ruling) · 기간(N일) 내
// 무효화 수. 무효화 상태는 status가 아니라 invalid_at 유무로 판정한다(가역 무효화).
// Doltgres 호환: FILTER 대신 CASE 집계(pattern.go 관례). T70: 컷오프를 Go에서
// 계산해 timestamptz 파라미터로 전달한다 — SQL의 ($1 * interval) 곱은 Doltgres가
// $1을 interval로 추론해 pgx 인코딩에 실패하고, ::int 캐스트로 넘겨도 평가가
// 깨진다(operator does not exist: timestamptz < interval, e2e 실측). maintain
// --stale과 동일 결함·동일 처방. 주의: report_test.go TestReportSQLDoltgresCompat
// 의 [90]·[days] int 인자 단언은 이 파라미터 곱 규약 자체를 검증한 것 — 룰링
// 대상(비소유 파일이라 여기서는 수정하지 않음).
func collectCounts(ctx context.Context, st Store, days int, out *reportOutput) error {
	rows, err := queryOnce(ctx, st, `
		SELECT count(CASE WHEN status = 'pending' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN status = 'active' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN invalid_at IS NOT NULL THEN 1 END)
		  FROM knowledge`, nil)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		readCounters(rows[0], &out.Counts.Pending, &out.Counts.Active, &out.Counts.Invalidated)
	}
	staleCutoff := time.Now().Add(-time.Duration(reportStaleDays) * 24 * time.Hour)
	rows, err = queryOnce(ctx, st, `
		SELECT count(CASE WHEN (last_read_at IS NOT NULL AND last_read_at < $1)
		                         OR (read_count = 0 AND ingested_at < $1) THEN 1 END),
		       count(CASE WHEN read_count = 0 AND ingested_at < $1 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`,
		[]any{staleCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		readCounters(rows[0], &out.Stale.Days90, &out.Stale.NeverRead)
	}
	periodCutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err = queryOnce(ctx, st, `
		SELECT count(*) FROM knowledge WHERE invalid_at >= $1`,
		[]any{periodCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) > 0 {
		readCounters(rows[0], &out.InvalidatedPeriod)
	}
	return nil
}

// readCounters — 집계 행의 숫자 컬럼(int32/int64/string 등)을 int로 읽는다.
// 변환은 cellToInt 단일 유틸을 공유한다(read.go — 3중 구현 방지). 매핑
// 불가 칸(nil·비수)은 기존값(0)을 유지한다.
func readCounters(row []any, dst ...*int) {
	for i, p := range dst {
		if i >= len(row) {
			return
		}
		if n, ok := cellToInt(row[i]); ok {
			*p = n
		}
	}
}

// pctOf — n/total 정수 백분율(버림). total 0은 0(readZeroPct 가드 관례).
func pctOf(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// collectEffect — 효과 계측 섹션 산출(v0.19.0 docs/METRICS.md §4·
// v0.20.0 정밀화). G1(hit·near)·G2(dup_prevented)는 collectDaily 합산에서
// 유도되고(여기서 재합산하지 않는다), G3(reuse·dead)은 knowledge 시점
// 집계 + hot 노출 보정(E), G4는 세션 경계 배정 페어링(D)·사용 연쇄(B) —
// 전부 Go-사이드 조인으로 산출해 SQL interval 곱의 Doltgres 파라미터
// 추론 리스크를 받지 않는다.
//
// 룰링(리뷰 #6): G3을 collectCounts의 stale 쿼리에 병합하지 않는다 —
// stale은 "인자 1개(timestamptz)" 계약을 T70 회귀 테스트가 검증하며
// dead 컷오프(30일)는 인자가 달라 계약을 깬다. 유지보수 경로의 소형
// 쿼리 추가를 대가로 두 관심을 분리 유지.
//
// 계측 원칙(§13·METRICS §4): 이벤트 로그(read_events·session_starts·
// hot_events) 조회 실패는 해당 지표를 0/폴백으로 강등할 뿐 보고 전체를
// 깨지 않는다. knowledge 조회 실패는 전파(다른 report 쿼리도 같이
// 실패할 테니).
func collectEffect(ctx context.Context, st Store, days int, out *reportOutput) error {
	now := time.Now()
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)

	// ── G3(+E 노출 보정) ──
	deadCutoff := now.Add(-time.Duration(reportDeadDays) * 24 * time.Hour)
	// 최근 hot 노출 집합 — 주입된 지식은 "회상 못 된 자산"이 아니라
	// "노출된 자산"(dead 분자 제외). 조회 실패는 공집합(보정 생략 강등).
	exposed := map[int]bool{}
	if rows, err := queryOnce(ctx, st,
		`SELECT ids FROM hot_events WHERE ts >= $1`, []any{deadCutoff}); err == nil {
		for _, r := range rows {
			if len(r) > 0 {
				if s, ok := r[0].(string); ok {
					for _, id := range parseIDsCSV(s) {
						exposed[id] = true
					}
				}
			}
		}
	}
	rows, err := queryOnce(ctx, st, `
		SELECT count(*),
		       count(CASE WHEN read_count > 0 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return err
	}
	var active, reused int
	if len(rows) > 0 {
		readCounters(rows[0], &active, &reused)
	}
	out.Effect.ReusePct = pctOf(reused, active)
	deadIDs, err := queryOnce(ctx, st, `
		SELECT id FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL
		   AND read_count = 0 AND ingested_at < $1`, []any{deadCutoff})
	if err != nil {
		return err
	}
	dead := 0
	for _, r := range deadIDs {
		if len(r) == 0 {
			continue
		}
		if id, ok := cellToInt(r[0]); ok && !exposed[id] {
			dead++
		}
	}
	out.Effect.DeadPct = pctOf(dead, active)

	// ── G4 ── 이벤트 로그 — 실패 시 G4 전량 0 강등.
	evs, err := queryReadEvents(ctx, st, windowStart)
	if err != nil {
		return nil
	}
	out.Effect.ReadEventsN = len(evs)
	fallbacks := 0
	for _, e := range evs {
		if e.fallback {
			fallbacks++
		}
	}
	out.Effect.FallbackPct = pctOf(fallbacks, len(evs))
	if len(evs) == 0 {
		return nil // 분모 0 — 페어링·연쇄 불필요(제로 가드 관례)
	}
	writes, err := queryWrites(ctx, st, windowStart)
	if err != nil {
		return nil // 동일 강등 — 페어링 불가, pairs 0
	}

	// 세션 경계(D) — 조회 실패·부재 시 시간창 폴백(구환경·첫일).
	starts, serr := queryTimes(ctx, st,
		`SELECT ts FROM session_starts WHERE ts >= $1`,
		windowStart.Add(-reportSessionLookback))
	hasSessions := serr == nil && len(starts) > 0

	// 페어링 — 같은 세션(배정된 양쪽)에서 read 직후 30분 내 write.
	// 미배정 이벤트·세션 부재 환경은 시간창 폴백(v0.19.0 규칙 승계).
	writeTimes := make([]time.Time, 0, len(writes))
	sessWriteTimes := map[int][]time.Time{}
	for _, w := range writes {
		writeTimes = append(writeTimes, w.ts)
		if hasSessions {
			if si := sessionIndexOf(starts, w.ts); si >= 0 {
				sessWriteTimes[si] = append(sessWriteTimes[si], w.ts)
			}
		}
	}
	pairs := 0
	for _, e := range evs {
		cands := writeTimes
		if hasSessions {
			if si := sessionIndexOf(starts, e.ts); si >= 0 {
				cands = sessWriteTimes[si]
			}
		}
		if timeWindowHit(cands, e.ts, reportPairWindow) {
			pairs++
		}
	}
	out.Effect.ReadWritePairs = pairs
	out.Effect.PairPct = pctOf(pairs, len(evs))

	// 회상 선도 세션 비율(D) — 창 내 시작 세션 중 기록(write)이 있고,
	// 그 세션의 첫 read가 첫 write보다 앞선 비율.
	if hasSessions {
		out.Effect.RecallLedPct = recallLedPct(starts, evs, writes, windowStart)
	}

	// 사용 연쇄(B) — 반환 id가 24h 내 재반환 또는 같은 topic write로
	// 이어진 read 비율. topic 맵은 active 전체 조회(개인 규모 — 수십~수백
	// 행, 창 이벤트 id만 필요하지만 맵 조회가 단순하다). 실패는 (b)
	// 경로만 강등.
	topics := map[int]string{}
	if rows, err := queryOnce(ctx, st,
		`SELECT id, topic FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`, nil); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			if id, ok := cellToInt(r[0]); ok {
				topics[id] = fmt.Sprint(r[1])
			}
		}
	}
	out.Effect.ChainPct = chainPct(evs, writes, topics)
	return nil
}

// readEventRow — read_events 1행의 계산 뷰(G4).
type readEventRow struct {
	ts       time.Time
	fallback bool
	ids      []int
}

// writeRow — 창 내 knowledge 커밋 1행의 계산 뷰(페어링·연쇄).
type writeRow struct {
	ts    time.Time
	topic string
}

// queryReadEvents — 창 내 read 이벤트를 ts 오름차순으로 읽는다(연쇄의
// 이후-판정이 순서를 가정). 매핑 불가 칸은 조용히 건너뛴다(cellToInt
// 관례 — 계측 누락이 보고를 깨지 않는다).
func queryReadEvents(ctx context.Context, st Store, cutoff time.Time) ([]readEventRow, error) {
	rows, err := queryOnce(ctx, st,
		`SELECT ts, fallback, ids FROM read_events WHERE ts >= $1 ORDER BY ts`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]readEventRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		t, ok := cellToTime(r[0])
		if !ok {
			continue
		}
		e := readEventRow{ts: t}
		if b, ok := r[1].(bool); ok {
			e.fallback = b
		}
		if len(r) > 2 {
			if s, ok := r[2].(string); ok {
				e.ids = parseIDsCSV(s)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// queryWrites — 창 내 knowledge 커밋(시각·topic)을 ts 오름차순으로 읽는다.
func queryWrites(ctx context.Context, st Store, cutoff time.Time) ([]writeRow, error) {
	rows, err := queryOnce(ctx, st,
		`SELECT ingested_at, topic FROM knowledge WHERE ingested_at >= $1 ORDER BY ingested_at`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]writeRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		t, ok := cellToTime(r[0])
		if !ok {
			continue
		}
		out = append(out, writeRow{ts: t, topic: fmt.Sprint(r[1])})
	}
	return out, nil
}

// queryTimes — 1열 시각 쿼리를 time 슬라이스로 읽는다(세션 경계 조회).
// 매핑 불가 칸은 조용히 건너뛴다(계측 누락이 보고를 깨지 않는다).
func queryTimes(ctx context.Context, st Store, sql string, cutoff time.Time) ([]time.Time, error) {
	rows, err := queryOnce(ctx, st, sql, []any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if t, ok := cellToTime(r[0]); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// cellToTime — 셀(any)→time 변환(pgx timestamptz=time.Time, fake·
// 문자열은 RFC3339). cellToInt 형제 유틸.
func cellToTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		p, err := time.Parse(time.RFC3339, t)
		return p, err == nil
	default:
		return time.Time{}, false
	}
}

// sessionIndexOf — starts(오름차순)에서 ts가 속한 세션 인덱스(마지막
// start ≤ ts). 첫 시작보다 앞이면 −1(미배정 — 폴백 페어링 대상).
func sessionIndexOf(starts []time.Time, ts time.Time) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i].After(ts) }) - 1
}

// timeWindowHit — base 직후 window 안(0 < Δ ≤ window)에 시각이 하나라도
// 있으면 참. times는 오름차순 정렬 가정.
func timeWindowHit(times []time.Time, base time.Time, window time.Duration) bool {
	i := sort.Search(len(times), func(i int) bool { return times[i].After(base) })
	return i < len(times) && !times[i].After(base.Add(window))
}

// recallLedPct — 창 내 시작 세션의 회상 선도율: 기록(write ≥1) 세션 중
// 첫 read가 첫 write보다 앞선(회상이 생산을 이끈) 세션 비율. 창 밖 시작
// 세션(마진분)은 제외, 미배정 이벤트는 그 세션에 귀속되지 않는다.
func recallLedPct(starts []time.Time, evs []readEventRow, writes []writeRow, windowStart time.Time) int {
	type sess struct {
		firstRead, firstWrite time.Time
		hasRead, hasWrite     bool
	}
	sessions := map[int]*sess{}
	for i, s := range starts {
		if !s.Before(windowStart) {
			sessions[i] = &sess{}
		}
	}
	for _, e := range evs {
		if si := sessionIndexOf(starts, e.ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasRead || e.ts.Before(ss.firstRead)) {
				ss.firstRead, ss.hasRead = e.ts, true
			}
		}
	}
	for _, w := range writes {
		if si := sessionIndexOf(starts, w.ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasWrite || w.ts.Before(ss.firstWrite)) {
				ss.firstWrite, ss.hasWrite = w.ts, true
			}
		}
	}
	active, led := 0, 0
	for _, ss := range sessions {
		if !ss.hasWrite {
			continue
		}
		active++
		if ss.hasRead && ss.firstRead.Before(ss.firstWrite) {
			led++
		}
	}
	return pctOf(led, active)
}

// chainPct — 사용 연쇄율(v0.20.0 B): 반환 id가 있는 read 중 24h 내
// (a) 이후 read에서 같은 id 재반환 또는 (b) 같은 topic write로 이어진
// 비율. evs·writes는 ts 오름차순 가정.
func chainPct(evs []readEventRow, writes []writeRow, topics map[int]string) int {
	withIDs, used := 0, 0
	for i, e := range evs {
		if len(e.ids) == 0 {
			continue
		}
		withIDs++
		idset := make(map[int]bool, len(e.ids))
		for _, id := range e.ids {
			idset[id] = true
		}
		hit := false
		// (a) 이후 24h 내 재반환.
		for j := i + 1; j < len(evs) && !hit; j++ {
			if evs[j].ts.After(e.ts.Add(reportChainWindow)) {
				break
			}
			for _, id := range evs[j].ids {
				if idset[id] {
					hit = true
					break
				}
			}
		}
		// (b) 이후 24h 내 같은 topic write.
		for _, w := range writes {
			if hit {
				break
			}
			if !w.ts.After(e.ts) || w.ts.After(e.ts.Add(reportChainWindow)) {
				continue
			}
			for _, id := range e.ids {
				if t := topics[id]; t != "" && t == w.topic {
					hit = true
					break
				}
			}
		}
		if hit {
			used++
		}
	}
	return pctOf(used, withIDs)
}
