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

	readCalls int // 직렬화 제외 — fallback_pct 분모(read_stats calls)
}

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
func collectDaily(ctx context.Context, st Store, days int, out *reportOutput) error {
	rows, err := queryOnce(ctx, st, `SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return err
	}
	window := make(map[string]int, days)
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- { // 오래된 날부터 — 오늘이 마지막 행
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		window[d] = len(out.Days)
		out.Days = append(out.Days, reportDay{Date: d})
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
		d.Date = out.Days[idx].Date
		out.Days[idx] = d
	}
	return nil
}

// collectStats — meta read_stats(zero_pct)·hot_stats(p95). 부재·파손은 0.
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
				Zero  int `json:"zero"`
			}
			if json.Unmarshal([]byte(fmt.Sprint(r[1])), &rs) != nil {
				continue
			}
			out.ZeroPct = readZeroPct(rs.Calls, rs.Zero)
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
