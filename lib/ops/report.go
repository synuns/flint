// report.go — T65 `flint report`: N일(기본 7·상한 14) 적립·유지보수 리포트를
// 한 줄 JSON으로 보고한다. 유지보수 경로(오프라인 CLI)라 핫패스 비간섭(§2).
// 데이터는 전부 wire.Store.Query로 읽는다 — meta daily:*·hot_stats와
// knowledge 집계(counts·stale·기간 무효화). 키 부재는 0, 파손 JSON은 스킵.
// v0.40.0 T3: lib/cli에서 이동(식별자 대문자화 — 동작·출력·exit 불변).
// v0.42.0 T2: read_stats blob 폐지 — 일별 read·zero·near는 daily:* 잔존값에
// read_events 파생값을 병합(mergeDerivedReadDaily), fallback_pct는 read_events
// 창 집계로 산출(CollectEffect). 지표 산출 값은 전환 전과 동등(동등성 테스트).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// reportDaysDefault — --days 기본 7. 상한은 보존 창(thresholds
// daily_retention_days, v0.24.0 R1b — 상수 reportDaysMax 폐지).
const reportDaysDefault = 7

// (v0.24.0 R1a: reportStaleDays 상수 폐지 — thresholds stale_days
// 키와 단일화, 오버라이드가 report 집계에도 반영된다.)

// ReportDay — 일별 유입 1행. 소스(hook/manual/mcp)와 dup는 write와 별도 증분.
// dup_* 3필드는 v0.10.2 R2 소스별 계측(미기록 일 0 — daily JSON 키 부재는
// zero 값으로 디코딩).
type ReportDay struct {
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

// ReportOutput — 한 줄 JSON 출력. 창 밖 날짜·부재 키·파손 키는 0행으로 채운다.
type ReportOutput struct {
	Days    []ReportDay `json:"days"`
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
	Effect EffectOutput `json:"effect"`
}

// EffectOutput — "도움" 지표(METRICS §2 GSM 표의 산출). hit_pct·near_pct·
// dup_prevented·read_events_n·read_write_pairs·pair_pct·fallback_pct·
// recall_led_pct·chain_pct는 --days 창, reuse_pct·dead_pct는 시점 지표
// (기간 아님). 전부 상관 프록시(Tier 1·2) — 세트로 해석하고 단독 증명으로
// 읽지 않는다(METRICS §5).
type EffectOutput struct {
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
	// v0.24.0 R4b: 프로파일 관측 힌트(D① 축소형) — 표본 하한 미달이면
	// "보류"(관측 선행 원칙 — 추측 금지, METRICS §5 준수: 보고 전용).
	ProfileHint string `json:"profile_hint"`
	// v0.30.0 R2 — hit 품질 근사(first_rank 기반, 폴백 제외).
	ExactPct int     `json:"exact_pct"` // 첫 topic 정합 반환율(정확일치 회수)
	Top1Pct  int     `json:"top1_pct"`  // 정합 1위율(정합 있는 read 중)
	MRR      float64 `json:"mrr"`       // 평균 역순위(정합 있는 read 중, 0~1)
	// v0.30.0 R1 — 7일 창 전후 추세(단면→추세, read_events 단일 표).
	Trend *TrendOutput `json:"trend,omitempty"`
}

// TrendOutput — 직전 7일(cur) vs 그 이전 7일(prev) 대비. delta=cur-prev.
type TrendOutput struct {
	HitPct      TrendPoint `json:"hit_pct"`
	FallbackPct TrendPoint `json:"fallback_pct"`
	Top1Pct     TrendPoint `json:"top1_pct"`
}

// TrendPoint — 전기·당기·증감(정수 pct).
type TrendPoint struct {
	Prev  int `json:"prev"`
	Cur   int `json:"cur"`
	Delta int `json:"delta"`
}

// v0.20.4(패치 루프 4차·마일스톤 B③): 효과 계측 판정 창은 thresholds
// 레지스트리(meta 오버라이드)로 이양 — 기본값 단일 소스는
// store.DefaultThresholds(dead_days 30·pair_window_minutes 30·
// chain_window_hours 24, TUNING §2).

// reportSessionLookback — 세션 배정 조회 마진: 창 시작 직전에 시작된
// 세션까지 포함해 창 첫 이벤트의 배정이 빠지지 않게 한다. 이보다 긴
// 세션에 속한 이벤트는 미배정 — 폴백 페어링(보수적).
const reportSessionLookback = 24 * time.Hour

// (v0.40.0 T3: cli별칭 DailyRow=ReportDay는 이동 폐기 — ops에는 status.go
// DailyRow(WindowedDailyFn seam 전달형)가 선행 존재한다. cli 잔여 소비자는
// cli/ops_alias_3.go가 ReportDay 별칭으로 잇는다.)

// DupPct — 기간 중복률: 기간 전체 합계 기반 Σdup/(Σwrite+Σdup),
// 버림 정수%. 제로 분모·빈 기간은 0.
func DupPct(days []ReportDay) int {
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

// FallbackPct — 폴백 비율: read_fallback_calls/read_calls, 버림 정수%.
// 제로 분모는 0.
func FallbackPct(fallbackCalls int, readCalls int) int {
	if readCalls == 0 {
		return 0
	}
	return int(100.0 * float64(fallbackCalls) / float64(readCalls))
}

// RunReport — --days 파싱 후 집계 조회·출력. 성공 0 · 인자 오류 1 ·
// 저장소 오류는 MapExit(서버 다운 2 등).
func RunReport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	days := reportDaysDefault
	htmlOut := ""
	daysSet := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--html": // v0.36.0(E): 정적 단일 파일 대시보드
			htmlOut = "report.html"
		case "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("report: --out requires a path")))
				return 3
			}
			htmlOut = args[i]
			if !strings.HasSuffix(htmlOut, ".html") {
				htmlOut += ".html"
			}
		case "--days":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("report: --days requires a number")))
				return 3
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("report: invalid days %q", args[i])))
				return 3
			}
			days = n
			daysSet = true
		default:
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 3
		}
	}
	// F6(성능): thresholds는 커맨드 스코프 1회 로드 — --days 상한·stale
	// 컷오프(collectCounts)·효과 판정 창(collectEffect)이 각자 meta IN
	// 쿼리를 날리던 것(실행당 최대 3회)을 공유 1회로. 폴백은 WriteThresholds
	// 관례(조회 실패 → DefaultThresholds) 불변. HTML 경로는 수집 체인
	// (report_html.go)이 자체 로드하므로 상한 적용(--days 지정)에만 쓴다.
	var th store.Thresholds
	if daysSet || htmlOut == "" {
		th = knowledge.WriteThresholds(ctx, st)
	}
	if daysSet {
		if max := th.DailyRetentionD; days > max {
			days = max // 상한 = 보존 창(그 너머 데이터는 존재하지 않는다)
		}
	}

	if htmlOut != "" { // v0.36.0: HTML 경로 — 동일 수집 체인 재사용
		return runReportHTML(ctx, st, days, htmlOut, stdout)
	}
	out := ReportOutput{Days: make([]ReportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if err := collectStats(ctx, st, &out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if err := collectCounts(ctx, st, days, th, &out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// v0.19.0 효과 계측 — METRICS §4 정의 산출(days 창·knowledge 시점).
	if err := collectEffect(ctx, st, days, th, &out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// R4 관측 지표 — dup_pct는 일별 행에서, fallback_pct는 CollectEffect의
	// read_events 창 집산에서(v0.42.0 T2 — 구 read_fallback_calls/read_stats
	// calls 누적 분모 폐지, effect.fallback_pct와 동일 산출).
	out.DupPct = DupPct(out.Days)
	// R2 소스별 dup 기간 합계 — 일별 행 합산(미기록 일은 0으로 합산됨).
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// collectDaily — meta daily:* 키를 N일 창(오늘 포함, UTC)에 맞춰 병합.
// 창 밖 키는 배제하고(8일치가 심여도 7행), 파손 JSON은 0행 폴백(스킵).
// v0.18.1: 창 병합 본체는 WindowedDaily 공유 리더로 추출 — status 기간화
// 지표(R4)와 파서를 단일화한다(포맷 변경 시 분기 위험 제거).
func collectDaily(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	dayRows, err := WindowedDaily(ctx, st, days)
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
	out.ZeroPct = ReadZeroPct(sumRead, sumZero)
	out.ZeroNearPct = ReadZeroPct(sumRead, sumNear)
	// v0.19.0 효과 뷰 G1·G2 — 같은 합산에서 유도(단일 유도 사슬 —
	// CollectEffect에서 재합산하지 않는다).
	out.Effect.HitPct = PctOf(sumRead-sumZero, sumRead)
	out.Effect.NearPct = PctOf(sumNear, sumRead)
	out.Effect.DupPrevented = sumDup
	return nil
}

// WindowedDaily — meta daily:* 키를 N일 창(오늘 포함, UTC) 행으로 병합해
// 반환(오래된 날부터 — 오늘이 마지막 행). 창 밖 키 배제·파손 JSON 0행
// 폴백. report(collectDaily)·status(statusWindowedRead) 양쪽의 단일
// 소스(v0.18.1 — daily 파서 3중 복제 해소).
func WindowedDaily(ctx context.Context, st wire.Store, days int) ([]ReportDay, error) {
	rows, err := wire.QueryOnce(ctx, st, `SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return nil, err
	}
	daysOut := make([]ReportDay, 0, days)
	window := make(map[string]int, days)
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- { // 오래된 날부터 — 오늘이 마지막 행
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		window[d] = len(daysOut)
		daysOut = append(daysOut, ReportDay{Date: d})
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
		var d ReportDay
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &d) != nil {
			continue // 파손 → 0 유지
		}
		d.Date = daysOut[idx].Date
		daysOut[idx] = d
	}
	mergeDerivedReadDaily(ctx, st, daysOut)
	return daysOut, nil
}

// mergeDerivedReadDaily — v0.42.0 T2: read 경로가 daily read 카운터를 더
// 쓰지 않는다(쓰기 4→2). 일별 read·read_zero·read_zero_near는 read_events를
// UTC 일자별로 모아 파생 산출해 daily:* 잔존값과 필드별 max 병합한다 —
// 전환 전 하루(양쪽 원천이 같은 이벤트를 가리키는 날)에는 값이 같아
// 이중 계산 없이 지표가 연속된다. 조회 실패는 잔존 daily 값 유지(계측
// 강등 원칙 — 보고를 깨지 않는다).
func mergeDerivedReadDaily(ctx context.Context, st wire.Store, days []ReportDay) {
	if len(days) == 0 {
		return
	}
	// 창 첫 날의 UTC 자정 — WindowedDaily 하루 행 전체를 포함하는 컷오프
	// (Go 계산 timestamptz 파라미터 — T70 관례, SQL interval 산산 금지).
	cutoff, err := time.Parse("2006-01-02", days[0].Date)
	if err != nil {
		return
	}
	rows, qerr := wire.QueryOnce(ctx, st,
		`SELECT ts, zero, near FROM read_events WHERE ts >= $1`, []any{cutoff})
	if qerr != nil {
		return
	}
	type readCnt struct{ read, zero, near int }
	byDay := map[string]*readCnt{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		ts, ok := CellToTime(r[0])
		if !ok {
			continue // 매핑 불가 칸은 조용히 건너뛴다(계측 관례)
		}
		key := ts.UTC().Format("2006-01-02")
		c := byDay[key]
		if c == nil {
			c = &readCnt{}
			byDay[key] = c
		}
		c.read++
		if cellToBool(r[1]) {
			c.zero++
		}
		if cellToBool(r[2]) {
			c.near++
		}
	}
	for i := range days {
		c := byDay[days[i].Date]
		if c == nil {
			continue
		}
		days[i].Read = max(days[i].Read, c.read)
		days[i].ReadZero = max(days[i].ReadZero, c.zero)
		days[i].ReadNear = max(days[i].ReadNear, c.near)
	}
}

// collectStats — meta hot_stats(p95). v0.42.0 T2 — read_stats(fallback_pct
// 분모 calls)는 폐지: fallback_pct는 CollectEffect의 read_events 창 집산에서
// 나온다. v0.15.0 R2 — zero_pct·zero_near_pct는 누적 read_stats가 아니라 daily
// 합산(collectDaily)에서 산출한다). 부재·파손은 0.
func collectStats(ctx context.Context, st wire.Store, out *ReportOutput) error {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('hot_stats')`, nil)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		switch fmt.Sprint(r[0]) {
		case "hot_stats":
			var hs struct {
				Lens []int `json:"lens"`
			}
			if json.Unmarshal([]byte(fmt.Sprint(r[1])), &hs) != nil {
				continue
			}
			lens := append([]int(nil), hs.Lens...)
			sort.Ints(lens)
			out.HotP95 = HotP95(lens)
		}
	}
	return nil
}

// CollectCounts — knowledge 집계 3종. pending·active(invalid_at 없음)·
// invalidated(총) · stale(days90·never_read 분리, §2 ruling) · 기간(N일) 내
// 무효화 수. 무효화 상태는 status가 아니라 invalid_at 유무로 판정한다(가역 무효화).
// Doltgres 호환: FILTER 대신 CASE 집계(pattern.go 관례). T70: 컷오프를 Go에서
// 계산해 timestamptz 파라미터로 전달한다 — SQL의 ($1 * interval) 곱은 Doltgres가
// $1을 interval로 추론해 pgx 인코딩에 실패하고, ::int 캐스트로 넘겨도 평가가
// 깨진다(operator does not exist: timestamptz < interval, e2e 실측). maintain
// --stale과 동일 결함·동일 처방. 주의: report_test.go TestReportSQLDoltgresCompat
// 의 [90]·[days] int 인자 단언은 이 파라미터 곱 규약 자체를 검증한 것 — 룰링
// 대상(비소유 파일이라 여기서는 수정하지 않음).
//
// F6(성능): CollectCounts는 공개 래퍼(직접 호출자 — report_html.go 조합
// 등 — 는 기존 시그니처로 자체 로드). RunReport 체인은 커맨드 스코프
// 1회 로드 값(th)을 주입받는 collectCounts를 쓴다.
func CollectCounts(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	return collectCounts(ctx, st, days, knowledge.WriteThresholds(ctx, st), out)
}

func collectCounts(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT count(CASE WHEN status = 'pending' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN status = 'active' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN invalid_at IS NOT NULL THEN 1 END)
		  FROM knowledge`, nil)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &out.Counts.Pending, &out.Counts.Active, &out.Counts.Invalidated)
	}
	staleCutoff := time.Now().Add(-time.Duration(th.StaleDays) * 24 * time.Hour)
	rows, err = wire.QueryOnce(ctx, st, `
		/* stale */ SELECT count(CASE WHEN (last_read_at IS NOT NULL AND last_read_at < $1)
		                         OR (read_count = 0 AND ingested_at < $1) THEN 1 END),
		       count(CASE WHEN read_count = 0 AND ingested_at < $1 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`,
		[]any{staleCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &out.Stale.Days90, &out.Stale.NeverRead)
	}
	periodCutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err = wire.QueryOnce(ctx, st, `
		SELECT count(*) FROM knowledge WHERE invalid_at >= $1`,
		[]any{periodCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) > 0 {
		ReadCounters(rows[0], &out.InvalidatedPeriod)
	}
	return nil
}

// ReadCounters — 집계 행의 숫자 컬럼(int32/int64/string 등)을 int로 읽는다.
// 변환은 render.CellToInt 단일 유틸을 공유한다(read.go — 3중 구현 방지). 매핑
// 불가 칸(nil·비수)은 기존값(0)을 유지한다.
func ReadCounters(row []any, dst ...*int) {
	for i, p := range dst {
		if i >= len(row) {
			return
		}
		if n, ok := render.CellToInt(row[i]); ok {
			*p = n
		}
	}
}

// PctOf — n/total 정수 백분율(버림). total 0은 0(ReadZeroPct 가드 관례).
func PctOf(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// CollectEffect — 효과 계측 섹션 산출(v0.19.0 docs/METRICS.md §4·
// v0.20.0 정밀화). G1(hit·near)·G2(dup_prevented)는 collectDaily 합산에서
// 유도되고(여기서 재합산하지 않는다), G3(reuse·dead)은 knowledge 시점
// 집계 + hot 노출 보정(E), G4는 세션 경계 배정 페어링(D)·사용 연쇄(B) —
// 전부 Go-사이드 조인으로 산출해 SQL interval 곱의 Doltgres 파라미터
// 추론 리스크를 받지 않는다.
//
// 룰링(리뷰 #6): G3을 CollectCounts의 stale 쿼리에 병합하지 않는다 —
// stale은 "인자 1개(timestamptz)" 계약을 T70 회귀 테스트가 검증하며
// dead 컷오프(30일)는 인자가 달라 계약을 깬다. 유지보수 경로의 소형
// 쿼리 추가를 대가로 두 관심을 분리 유지.
//
// 계측 원칙(§13·METRICS §4): 이벤트 로그(read_events·session_starts·
// hot_events) 조회 실패는 해당 지표를 0/폴백으로 강등할 뿐 보고 전체를
// 깨지 않는다. knowledge 조회 실패는 전파(다른 report 쿼리도 같이
// 실패할 테니).
//
// F6(성능): CollectEffect는 공개 래퍼(직접 호출자 — report_html.go 조합
// 등 — 은 기존 시그니처로 자체 로드). RunReport 체인은 커맨드 스코프
// 1회 로드 값(th)을 주입받는 collectEffect를 쓴다.
func CollectEffect(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	return collectEffect(ctx, st, days, knowledge.WriteThresholds(ctx, st), out)
}

func collectEffect(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	now := time.Now()
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)
	// 판정 창 임계값(v0.20.4 이양) — 호출자 주입(th, F6: 커맨드 스코프
	// 1회 로드 공유 — 로드 실패 폴백은 WriteThresholds가 이미 적용).
	pairWindow := time.Duration(th.PairWindowMin) * time.Minute
	chainWindow := time.Duration(th.ChainWindowH) * time.Hour

	// ── G3(+노출 정합 가드) ──
	deadCutoff := now.Add(-time.Duration(th.DeadDays) * 24 * time.Hour)
	// 최근 hot 노출 집합 — hot --full은 BumpReads로 주입 id의 read_count를
	// 올리므로(T62 참조 계측) 정상 경로에서 노출 지식은 애초에 read_count=0
	// 후보 밖이다. 이 가드는 참조 증분과 노출 기록이 별도 경로라 계측
	// 유실(증분만 실패) 시 생기는 불일치를 메운다(리뷰 #2 재정의).
	// 조회 실패는 공집합(가드 생략 강등).
	exposed := map[int]bool{}
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT ids FROM hot_events WHERE ts >= $1`, []any{deadCutoff}); err == nil {
		for _, r := range rows {
			if len(r) > 0 {
				if s, ok := r[0].(string); ok {
					for _, id := range knowledge.ParseIDsCSV(s) {
						exposed[id] = true
					}
				}
			}
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT count(*),
		       count(CASE WHEN read_count > 0 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return err
	}
	var active, reused int
	if len(rows) > 0 {
		ReadCounters(rows[0], &active, &reused)
	}
	out.Effect.ReusePct = PctOf(reused, active)
	deadIDs, err := wire.QueryOnce(ctx, st, `
		/* dead */ SELECT id FROM knowledge
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
		if id, ok := render.CellToInt(r[0]); ok && !exposed[id] {
			dead++
		}
	}
	out.Effect.DeadPct = PctOf(dead, active)

	// ── G4 ── 이벤트 로그 — 실패 시 G4 전량 0 강등.
	// v0.30.0 리뷰 3: 페치 창은 max(days, 14일) — 추세 prev 창(직전
	// 7일)이 보고창 컷오프에 잘려 trend가 기본 --days 7에서 영원히
	// 공란이던 결함. 집계는 각 지표 창(Σ는 windowStart 이후) 그대로.
	fetchStart := windowStart
	if min14 := now.Add(-14 * 24 * time.Hour); min14.Before(fetchStart) {
		fetchStart = min14
	}
	evs, err := queryReadEvents(ctx, st, fetchStart)
	if err != nil {
		return nil
	}
	// 보고창 내 이벤트만 G4 지표 분모로(페치는 추세용으로 14일 — 위).
	inWin := make([]ReadEventRow, 0, len(evs))
	for _, e := range evs {
		if !e.Ts.Before(windowStart) {
			inWin = append(inWin, e)
		}
	}
	out.Effect.ReadEventsN = len(inWin)
	fallbacks := 0
	for _, e := range inWin {
		if e.Fallback {
			fallbacks++
		}
	}
	out.Effect.FallbackPct = PctOf(fallbacks, len(inWin)) // v0.31.3(감사 1): 분모도 보고창
	// v0.42.0 T2 — 최상위 fallback_pct도 동일 산출로 이관(구 누적
	// read_fallback_calls/read_stats calls 분모 폐지 — HTML 경로 포함
	// 단일 소스. 동등성: 같은 창·같은 이벤트 원천이라 값 불변).
	out.FallbackPct = out.Effect.FallbackPct
	// v0.30.0 R2 — hit 품질 근사(first_rank, 폴백 제외 계약). 리뷰 8:
	// 정합/top1 정의는 trendOf와 단일 구현을 공유한다.
	q := qualityOf(inWin)
	out.Effect.ExactPct = PctOf(q.exact, len(inWin))
	out.Effect.Top1Pct = PctOf(q.top1, q.exact)
	out.Effect.MRR = math.Round(q.rrSum/math.Max(float64(q.exact), 1)*1000) / 1000
	// v0.30.0 R1 — 7일 창 추세(cur=최근 7일, prev=직전 7일). 표본 부족
	// 창(0 이벤트)은 0으로 강등(§13 — 빈 창에 해석 없음).
	cur, prev := TrendWindow(now, evs)
	out.Effect.Trend = cur.Diff(prev)
	if len(inWin) == 0 {
		// v0.33.0(패치 감사 3): 빈 창에도 힌트는 "보류(표본 부족)"로
		// 완결 — 공백 영구화 패턴(v0.24.1과 동일 부류) 수선.
		out.Effect.ProfileHint = ProfileHint(th, out.Effect)
		return nil // 분모 0 — 페어링·연쇄 불필요(제로 가드 관례)
		// v0.31.3(감사 1~3): G4 집계(pair·chain·recall_led)는 전부
		// 보고창(inWin) 소스 — 페치(14일, 추세용) 누유 금지.
	}
	writes, err := queryWrites(ctx, st, windowStart)
	if err != nil {
		return nil // 동일 강등 — 페어링 불가, pairs 0
	}

	// 세션 경계(D) — 조회 실패·부재 시 시간창 폴백(구환경·첫일).
	starts, serr := queryTimes(ctx, st,
		`SELECT ts FROM session_starts WHERE ts >= $1 ORDER BY ts`,
		windowStart.Add(-reportSessionLookback))
	hasSessions := serr == nil && len(starts) > 0

	// 페어링 — 같은 세션(배정된 양쪽)에서 read 직후 30분 내 write.
	// 미배정 이벤트·세션 부재 환경은 시간창 폴백(v0.19.0 규칙 승계).
	writeTimes := make([]time.Time, 0, len(writes))
	sessWriteTimes := map[int][]time.Time{}
	for _, w := range writes {
		writeTimes = append(writeTimes, w.Ts)
		if hasSessions {
			if si := sessionIndexOf(starts, w.Ts); si >= 0 {
				sessWriteTimes[si] = append(sessWriteTimes[si], w.Ts)
			}
		}
	}
	pairs := 0
	for _, e := range inWin { // v0.31.3(감사 2): 보고창 이벤트만 페어링
		cands := writeTimes
		if hasSessions {
			if si := sessionIndexOf(starts, e.Ts); si >= 0 {
				cands = sessWriteTimes[si]
			}
		}
		if TimeWindowHit(cands, e.Ts, pairWindow) {
			pairs++
		}
	}
	out.Effect.ReadWritePairs = pairs
	out.Effect.PairPct = PctOf(pairs, len(inWin))

	// 회상 선도 세션 비율(D) — 창 내 시작 세션 중 기록(write)이 있고,
	// 그 세션의 첫 read가 첫 write보다 앞선 비율.
	if hasSessions {
		out.Effect.RecallLedPct = recallLedPct(starts, inWin, writes, windowStart)
	}

	// 사용 연쇄(B) — 반환 id가 24h 내 재반환 또는 같은 topic write로
	// 이어진 read 비율. ids를 가진 이벤트가 없으면 topics 조회를 건너뛴다
	// (분모 0 확정 — 불필요한 스캔 제거, 리뷰 #6). topic 맵은 전체 knowledge
	// 조회(무효화·대체 지식도 연쇄 매칭에 필요 — read 시점엔 active였을
	// 수 있다, 리뷰 #4). 조회 실패는 (b) 경로만 강등.
	// v0.24.1(패치 루프 1차 관측): 힌트는 표본 수만으로 판정 — ids
	// 유무와 무관하게 가드 앞에서 산출한다(이전엔 hasIDs 조기 return
	// 뒤에 있어 zero-hit read만 있는 운영에서 영구 공백).
	out.Effect.ProfileHint = ProfileHint(th, out.Effect)
	hasIDs := false
	for _, e := range inWin {
		if len(e.Ids) > 0 {
			hasIDs = true
			break
		}
	}
	if !hasIDs {
		return nil
	}
	topics := map[int]string{}
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge`, nil); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			if id, ok := render.CellToInt(r[0]); ok {
				topics[id] = fmt.Sprint(r[1])
			}
		}
	}
	out.Effect.ChainPct = ChainPct(inWin, writes, topics, chainWindow) // v0.31.3(감사 3): 보고창 소스
	return nil
}

// ProfileHint — D① 축소형: 관측 기반 프로파일 힌트(보고 전용 — 자동
// 전환 금지 METRICS §5). 표본 하한(회상 이벤트 10 — zero-near 표본
// 관례 승계) 미달은 "보류", 이후 recall_led_pct 기반 안내.
func ProfileHint(th store.Thresholds, e EffectOutput) string {
	// 표본 하한은 zero-near 표본 관례(zero_near_min_read) 승계 — 튜닝 가능.
	if e.ReadEventsN < th.ZeroNearMinRead {
		return "보류(표본 부족 — 관측 선행)"
	}
	if e.RecallLedPct >= 50 {
		return "normal 유지 근거 충분(회상 선도 다수)"
	}
	return "회상 선도 낮음 — minimal 전환 고려 가능(안내만, 자동 전환 없음)"
}

// ReadEventRow — read_events 1행의 계산 뷰(G4).
type ReadEventRow struct {
	Ts        time.Time
	Zero      bool
	Fallback  bool
	FirstRank int
	Ids       []int
}

// cellToBool — 셀 bool 변환(문자열 "true"/bool 양쪽 수용).
func cellToBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fmt.Sprint(v) == "true"
}

// TrendStat — 창 내 이벤트의 (hit%, fallback%, top1%).
type TrendStat struct {
	HitPct, FallbackPct, Top1Pct int
	N                            int
}

// TrendWindow — cur(최근 7일)·prev(직전 7일) 창 통계.
func TrendWindow(now time.Time, evs []ReadEventRow) (cur, prev TrendStat) {
	cut := now.Add(-7 * 24 * time.Hour)
	prevCut := now.Add(-14 * 24 * time.Hour)
	var ce, pe []ReadEventRow
	for _, e := range evs {
		switch {
		case !e.Ts.Before(cut):
			ce = append(ce, e)
		case e.Ts.Before(cut) && !e.Ts.Before(prevCut):
			pe = append(pe, e)
		}
	}
	return trendOf(ce), trendOf(pe)
}

// qualityCounts — first_rank 기반 품질 집계(정합·1위·역순위 합).
// 정의 단일 소스: effect 지표·trendOf 양쪽이 공유(v0.30.0 리뷰 8).
type qualityCounts struct {
	exact, top1 int
	rrSum       float64
}

func qualityOf(evs []ReadEventRow) qualityCounts {
	var q qualityCounts
	for _, e := range evs {
		if e.FirstRank > 0 {
			q.exact++
			q.rrSum += 1.0 / float64(e.FirstRank)
			if e.FirstRank == 1 {
				q.top1++
			}
		}
	}
	return q
}

// trendOf — 이벤트 슬라이스 통계(빈 창은 0).
func trendOf(evs []ReadEventRow) TrendStat {
	if len(evs) == 0 {
		return TrendStat{}
	}
	hit, fb := 0, 0
	for _, e := range evs {
		if !e.Zero {
			hit++
		}
		if e.Fallback {
			fb++
		}
	}
	q := qualityOf(evs)
	return TrendStat{
		HitPct: PctOf(hit, len(evs)), FallbackPct: PctOf(fb, len(evs)),
		Top1Pct: PctOf(q.top1, q.exact), N: len(evs),
	}
}

// diff — 전기 대비 당기 추세 포인트(빈 창은 0 강등 — 표본 없는 비교 금지).
func (cur TrendStat) Diff(prev TrendStat) *TrendOutput {
	c, p := cur, prev
	if c.N == 0 || p.N == 0 {
		return nil
	}
	tp := func(a, b int) TrendPoint { return TrendPoint{Prev: b, Cur: a, Delta: a - b} }
	return &TrendOutput{
		HitPct:      tp(c.HitPct, p.HitPct),
		FallbackPct: tp(c.FallbackPct, p.FallbackPct),
		Top1Pct:     tp(c.Top1Pct, p.Top1Pct),
	}
}

// WriteRow — 창 내 knowledge 커밋 1행의 계산 뷰(페어링·연쇄).
type WriteRow struct {
	Ts    time.Time
	Topic string
}

// queryReadEvents — 창 내 read 이벤트를 ts 오름차순으로 읽는다(연쇄의
// 이후-판정이 순서를 가정). 매핑 불가 칸은 조용히 건너뛴다(render.CellToInt
// 관례 — 계측 누락이 보고를 깨지 않는다).
func queryReadEvents(ctx context.Context, st wire.Store, cutoff time.Time) ([]ReadEventRow, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT ts, zero, fallback, ids, first_rank FROM read_events WHERE ts >= $1 ORDER BY ts`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]ReadEventRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		t, ok := CellToTime(r[0])
		if !ok {
			continue
		}
		// v0.30.0 리뷰 1·2: 열 순서 (ts, zero, fallback, ids, first_rank) —
		// 구 SELECT(ts,fallback,ids) 잔여 파싱 2블록이 fallback=zero 오염·
		// ids 미파싱(연쇄 붕괴)을 냈다. 단일 경로로 정리.
		e := ReadEventRow{Ts: t, Zero: cellToBool(r[1]), Fallback: cellToBool(r[2])}
		if len(r) > 3 {
			if s, ok := r[3].(string); ok {
				e.Ids = knowledge.ParseIDsCSV(s)
			}
		}
		if len(r) >= 5 {
			if n, ok := render.CellToInt(r[4]); ok {
				e.FirstRank = n
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// queryWrites — 창 내 knowledge 커밋(시각·topic)을 ts 오름차순으로 읽는다.
func queryWrites(ctx context.Context, st wire.Store, cutoff time.Time) ([]WriteRow, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT ingested_at, topic FROM knowledge WHERE ingested_at >= $1 ORDER BY ingested_at`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]WriteRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		t, ok := CellToTime(r[0])
		if !ok {
			continue
		}
		out = append(out, WriteRow{Ts: t, Topic: fmt.Sprint(r[1])})
	}
	return out, nil
}

// queryTimes — 1열 시각 쿼리를 time 슬라이스로 읽는다(세션 경계 조회).
// 매핑 불가 칸은 조용히 건너뛴다(계측 누락이 보고를 깨지 않는다).
func queryTimes(ctx context.Context, st wire.Store, sql string, cutoff time.Time) ([]time.Time, error) {
	rows, err := wire.QueryOnce(ctx, st, sql, []any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if t, ok := CellToTime(r[0]); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// CellToTime — 셀(any)→time 변환. v0.41.0 T4: wire.CellToTime 단일
// 구현 별칭(cli 소비자 호환 export 유지 — 동일 계약).
var CellToTime = wire.CellToTime

// sessionIndexOf — starts(오름차순)에서 ts가 속한 세션 인덱스(마지막
// start ≤ ts). 첫 시작보다 앞이면 −1(미배정 — 폴백 페어링 대상).
func sessionIndexOf(starts []time.Time, ts time.Time) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i].After(ts) }) - 1
}

// TimeWindowHit — base 직후 window 안(0 < Δ ≤ window)에 시각이 하나라도
// 있으면 참. times는 오름차순 정렬 가정.
func TimeWindowHit(times []time.Time, base time.Time, window time.Duration) bool {
	i := sort.Search(len(times), func(i int) bool { return times[i].After(base) })
	return i < len(times) && !times[i].After(base.Add(window))
}

// recallLedPct — 창 내 시작 세션의 회상 선도율: 기록(write ≥1) 세션 중
// 첫 read가 첫 write보다 앞선(회상이 생산을 이끈) 세션 비율. 창 밖 시작
// 세션(마진분)은 제외, 미배정 이벤트는 그 세션에 귀속되지 않는다.
func recallLedPct(starts []time.Time, evs []ReadEventRow, writes []WriteRow, windowStart time.Time) int {
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
		if si := sessionIndexOf(starts, e.Ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasRead || e.Ts.Before(ss.firstRead)) {
				ss.firstRead, ss.hasRead = e.Ts, true
			}
		}
	}
	for _, w := range writes {
		if si := sessionIndexOf(starts, w.Ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasWrite || w.Ts.Before(ss.firstWrite)) {
				ss.firstWrite, ss.hasWrite = w.Ts, true
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
	return PctOf(led, active)
}

// ChainPct — 사용 연쇄율(v0.20.0 B): 반환 id가 있는 read 중 24h 내
// (a) 이후 read에서 같은 id 재반환 또는 (b) 같은 topic write로 이어진
// 비율. evs·writes는 ts 오름차순 가정.
func ChainPct(evs []ReadEventRow, writes []WriteRow, topics map[int]string, window time.Duration) int {
	withIDs, used := 0, 0
	for i, e := range evs {
		if len(e.Ids) == 0 {
			continue
		}
		withIDs++
		idset := make(map[int]bool, len(e.Ids))
		for _, id := range e.Ids {
			idset[id] = true
		}
		hit := false
		// (a) 이후 24h 내 재반환 — Δ>0 필요(페어링의 0 < Δ 규칙과 일관,
		// 동시 기록(Δ=0)은 연쇄 아님, 리뷰 #5).
		for j := i + 1; j < len(evs) && !hit; j++ {
			if evs[j].Ts.After(e.Ts.Add(window)) {
				break
			}
			if !evs[j].Ts.After(e.Ts) {
				continue
			}
			for _, id := range evs[j].Ids {
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
			if !w.Ts.After(e.Ts) || w.Ts.After(e.Ts.Add(window)) {
				continue
			}
			for _, id := range e.Ids {
				if t := topics[id]; t != "" && t == w.Topic {
					hit = true
					break
				}
			}
		}
		if hit {
			used++
		}
	}
	return PctOf(used, withIDs)
}
