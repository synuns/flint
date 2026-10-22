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
type reportDay struct {
	Date   string `json:"date"`
	Write  int    `json:"write"`
	Hook   int    `json:"hook"`
	Manual int    `json:"manual"`
	Mcp    int    `json:"mcp"`
	Dup    int    `json:"dup"`
	Hot    int    `json:"hot"`
	Read   int    `json:"read"`
}

// reportOutput — 한 줄 JSON 출력. 창 밖 날짜·부재 키·파손 키는 0행으로 채운다.
type reportOutput struct {
	Days              []reportDay `json:"days"`
	ZeroPct           int         `json:"zero_pct"`
	HotP95            int         `json:"hot_p95"`
	Counts            struct {
		Pending     int `json:"pending"`
		Active      int `json:"active"`
		Invalidated int `json:"invalidated"`
	} `json:"counts"`
	Stale struct {
		Days90    int `json:"days90"`
		NeverRead int `json:"never_read"`
	} `json:"stale"`
	InvalidatedPeriod int `json:"invalidated_period"`
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
func collectCounts(ctx context.Context, st Store, days int, out *reportOutput) error {
	rows, err := queryOnce(ctx, st, fmt.Sprintf(`
		SELECT count(*) FILTER (WHERE status = 'pending' AND invalid_at IS NULL),
		       count(*) FILTER (WHERE status = 'active' AND invalid_at IS NULL),
		       count(*) FILTER (WHERE invalid_at IS NOT NULL)
		  FROM knowledge`), nil)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		readCounters(rows[0], &out.Counts.Pending, &out.Counts.Active, &out.Counts.Invalidated)
	}
	rows, err = queryOnce(ctx, st, fmt.Sprintf(`
		SELECT count(*) FILTER (WHERE last_read_at < now() - interval '%d days'
		                         OR (read_count = 0 AND ingested_at < now() - interval '%d days')),
		       count(*) FILTER (WHERE read_count = 0 AND ingested_at < now() - interval '%d days')
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`, reportStaleDays, reportStaleDays, reportStaleDays), nil)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		readCounters(rows[0], &out.Stale.Days90, &out.Stale.NeverRead)
	}
	rows, err = queryOnce(ctx, st, `
		SELECT count(*) FROM knowledge WHERE invalid_at >= now() - make_interval(days => $1)`,
		[]any{days})
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) > 0 {
		readCounters(rows[0], &out.InvalidatedPeriod)
	}
	return nil
}

// readCounters — 집계 행의 숫자 컬럼(int16/int32/int64/string 등)을 int로 읽는다.
func readCounters(row []any, dst ...*int) {
	for i, p := range dst {
		if i >= len(row) {
			return
		}
		fmt.Sscan(fmt.Sprint(row[i]), p)
	}
}
